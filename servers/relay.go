// Copyright (c) 2026 Visvasity LLC

package servers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/visvasity/httphelp"
	"github.com/visvasity/syncmap"
)

// relayInfo is the per-relay registry value for both explicit relays and per-user
// delegations. CleanURL, when set, makes the proxy rewrite the outgoing Host to
// the literal backend host ("127.0.0.1" for tcp, "unix" for unix) instead of the
// <name>.localhost form.
type relayInfo struct {
	Target   string `json:"target"`
	CleanURL bool   `json:"clean_url,omitempty"`
}

type Relay struct {
	lifeCtx    context.Context
	lifeCancel context.CancelCauseFunc

	wg sync.WaitGroup

	opts *Options

	dataDir string

	// relays maps an explicit-relay name to its target; kept in sync with
	// relaysPath.
	relays     syncmap.Map[string, relayInfo]
	relaysPath string

	// users maps a username to the Unix socket its <*>.<user>.localhost requests
	// are delegated to; kept in sync with usersPath.
	users     syncmap.Map[string, relayInfo]
	usersPath string

	controlPath string

	startStopMutex sync.Mutex
	httpServer     *http.Server
	httpsServer    *http.Server
	controlServer  *http.Server
	forwardServer  *http.Server

	rproxy       *httphelp.Server
	targetMap    syncmap.Map[string, http.Handler]
	userProxyMap syncmap.Map[string, http.Handler]

	// alive holds the latest per-target liveness (target -> up), refreshed by the
	// background health checker and read when building list entries.
	alive atomic.Pointer[map[string]bool]
}

func New(dataDir string, opts *Options) (_ *Relay, err error) {
	if opts == nil {
		opts = new(Options)
	}
	opts.setDefaults()
	if err := opts.Check(context.TODO()); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create data directory: %w", err)
	}
	if err := os.MkdirAll(opts.SocketsDir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create sockets directory: %w", err)
	}
	lifeCtx, lifeCancel := context.WithCancelCause(context.Background())
	u := &Relay{
		lifeCtx:     lifeCtx,
		lifeCancel:  lifeCancel,
		opts:        opts,
		dataDir:     dataDir,
		relaysPath:  filepath.Join(dataDir, "relays.json"),
		usersPath:   filepath.Join(dataDir, "users.json"),
		controlPath: filepath.Join(opts.SocketsDir, ReservedName+".sock"),
	}
	if err := u.loadRelays(); err != nil {
		return nil, err
	}
	if err := u.loadUsers(); err != nil {
		return nil, err
	}
	return u, nil
}

// relayDial parses a relay target URL into a dial network, address, and scheme.
// The scheme is http or https (whether the backend hop uses TLS); a host means a
// loopback TCP backend, an absolute path (empty host) means a Unix socket, and
// exactly one of the two must be present.
func relayDial(target string) (network, address, scheme string, err error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid target %q: %w", target, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", "", fmt.Errorf("target %q must use the http or https scheme", target)
	}
	scheme = u.Scheme

	path := u.Path
	if path == "/" {
		path = "" // a bare slash is treated as no path
	}
	hasHost, hasPath := u.Host != "", path != ""

	switch {
	case hasHost && hasPath:
		return "", "", "", fmt.Errorf("target %q must name either a host (tcp) or an absolute path (unix socket), not both", target)
	case !hasHost && !hasPath:
		return "", "", "", fmt.Errorf("target %q must name a host (tcp) or an absolute path (unix socket)", target)
	case hasHost:
		host := u.Hostname()
		if host == "" {
			host = "127.0.0.1"
		}
		port := u.Port()
		if port == "" {
			return "", "", "", fmt.Errorf("tcp target %q must include a port", target)
		}
		if host != "localhost" {
			// IPv4 loopback only; IPv6 targets are deferred.
			if ip := net.ParseIP(host); ip == nil || ip.To4() == nil || !ip.IsLoopback() {
				return "", "", "", fmt.Errorf("tcp target %q host must be an IPv4 loopback address (127.0.0.0/8) or localhost", target)
			}
		}
		return "tcp", net.JoinHostPort(host, port), scheme, nil
	default:
		if !strings.HasPrefix(path, "/") {
			return "", "", "", fmt.Errorf("unix target %q path must be absolute", target)
		}
		return "unix", path, scheme, nil
	}
}

// loadRelays restores the persisted relays into the in-memory map. A missing file
// is not an error.
func (r *Relay) loadRelays() error {
	data, err := os.ReadFile(r.relaysPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m map[string]relayInfo
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("could not parse relay registry %q: %w", r.relaysPath, err)
	}
	for name, info := range m {
		r.relays.Store(name, info)
	}
	return nil
}

// saveRelays writes the in-memory relays to disk durably (temp file + rename) at
// mode 0600.
func (r *Relay) saveRelays() error {
	m := make(map[string]relayInfo)
	r.relays.Range(func(name string, info relayInfo) bool {
		m[name] = info
		return true
	})
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.relaysPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.relaysPath)
}

// loadUsers restores the persisted user delegations into the in-memory map. A
// missing file is not an error.
func (r *Relay) loadUsers() error {
	data, err := os.ReadFile(r.usersPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m map[string]relayInfo
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("could not parse user registry %q: %w", r.usersPath, err)
	}
	for name, info := range m {
		r.users.Store(name, info)
	}
	return nil
}

// saveUsers writes the in-memory user delegations to disk durably (temp file +
// rename) at mode 0600.
func (r *Relay) saveUsers() error {
	m := make(map[string]relayInfo)
	r.users.Range(func(name string, info relayInfo) bool {
		m[name] = info
		return true
	})
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.usersPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.usersPath)
}

// handleAdd registers or replaces an explicit relay, or a per-user delegation
// when req.User is set.
func (r *Relay) handleAdd(ctx context.Context, req *AddRequest) (*AddResponse, error) {
	if req.User {
		return r.handleAddUser(req)
	}
	if err := ValidName(req.Name); err != nil {
		return &AddResponse{Err: err.Error()}, nil
	}
	if req.Name == ReservedName {
		return &AddResponse{Err: fmt.Sprintf("%q is a reserved name", ReservedName)}, nil
	}
	if _, ok := r.users.Load(req.Name); ok {
		return &AddResponse{Err: fmt.Sprintf("name %q is already registered as a user", req.Name)}, nil
	}
	if _, _, _, err := relayDial(req.Target); err != nil {
		return &AddResponse{Err: err.Error()}, nil
	}
	r.relays.Store(req.Name, relayInfo{Target: req.Target, CleanURL: req.CleanURL})
	// Drop any cached proxy so the new relay takes precedence over a conventional
	// socket of the same name.
	r.targetMap.Delete(req.Name)
	if err := r.saveRelays(); err != nil {
		return &AddResponse{Err: err.Error()}, nil
	}
	return &AddResponse{}, nil
}

// newRelayProxy builds a reverse proxy for the relay named name, resolving the
// target from the registry at dial time so a later add (replace) takes effect.
func (r *Relay) newRelayProxy(name string) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			scheme := "http"
			host := name + ".localhost"
			if info, ok := r.relays.Load(name); ok {
				if network, _, s, err := relayDial(info.Target); err == nil {
					scheme = s
					if info.CleanURL {
						host = cleanHost(network)
					}
				}
			}
			pr.Out.URL.Scheme = scheme
			pr.Out.URL.Host = host
			// ReverseProxy sends Out.Host (cloned from the inbound request) in
			// preference to Out.URL.Host, so set it explicitly.
			pr.Out.Host = host
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				info, ok := r.relays.Load(name)
				if !ok {
					return nil, fmt.Errorf("no relay for %q", name)
				}
				network, address, _, err := relayDial(info.Target)
				if err != nil {
					return nil, err
				}
				return (&net.Dialer{}).DialContext(ctx, network, address)
			},
			// Backends are local; an https backend is typically self-signed and
			// won't match the <name>.localhost SNI, so the TLS hop is encrypted
			// but not verified.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// handleAddUser registers or replaces a per-user subdomain delegation. The
// socket's existence and ownership are not checked here; that happens on every
// request at dispatch time.
func (r *Relay) handleAddUser(req *AddRequest) (*AddResponse, error) {
	if err := ValidName(req.Name); err != nil {
		return &AddResponse{Err: err.Error()}, nil
	}
	if req.Name == ReservedName {
		return &AddResponse{Err: fmt.Sprintf("%q is a reserved name", ReservedName)}, nil
	}
	if _, ok := r.relays.Load(req.Name); ok {
		return &AddResponse{Err: fmt.Sprintf("name %q is already registered as a relay", req.Name)}, nil
	}
	if _, err := user.Lookup(req.Name); err != nil {
		return &AddResponse{Err: fmt.Sprintf("unknown user %q: %v", req.Name, err)}, nil
	}
	if !filepath.IsAbs(req.Target) {
		return &AddResponse{Err: fmt.Sprintf("user target %q must be an absolute unix socket path", req.Target)}, nil
	}
	r.users.Store(req.Name, relayInfo{Target: req.Target, CleanURL: req.CleanURL})
	r.userProxyMap.Delete(req.Name)
	if err := r.saveUsers(); err != nil {
		return &AddResponse{Err: err.Error()}, nil
	}
	return &AddResponse{}, nil
}

// handleRemove deletes an explicit relay, or a per-user delegation when req.User
// is set. It is the inverse of handleAdd.
func (r *Relay) handleRemove(ctx context.Context, req *RemoveRequest) (*RemoveResponse, error) {
	if req.User {
		return r.handleRemoveUser(req)
	}
	if err := ValidName(req.Name); err != nil {
		return &RemoveResponse{Err: err.Error()}, nil
	}
	if _, ok := r.relays.LoadAndDelete(req.Name); !ok {
		return &RemoveResponse{Err: fmt.Sprintf("no relay named %q", req.Name)}, nil
	}
	r.targetMap.Delete(req.Name)
	if err := r.saveRelays(); err != nil {
		return &RemoveResponse{Err: err.Error()}, nil
	}
	return &RemoveResponse{}, nil
}

// handleRemoveUser deletes a per-user subdomain delegation.
func (r *Relay) handleRemoveUser(req *RemoveRequest) (*RemoveResponse, error) {
	if err := ValidName(req.Name); err != nil {
		return &RemoveResponse{Err: err.Error()}, nil
	}
	if _, ok := r.users.LoadAndDelete(req.Name); !ok {
		return &RemoveResponse{Err: fmt.Sprintf("no user delegation for %q", req.Name)}, nil
	}
	r.userProxyMap.Delete(req.Name)
	if err := r.saveUsers(); err != nil {
		return &RemoveResponse{Err: err.Error()}, nil
	}
	return &RemoveResponse{}, nil
}

// handleList returns all active relays sorted by name: explicit relays, per-user
// delegations, and the automatic relays backed by <name>.sock files in the
// sockets directory (excluding any shadowed by an explicit relay of the same
// name).
func (r *Relay) handleList(ctx context.Context, req *ListRequest) (*ListResponse, error) {
	return &ListResponse{Entries: r.listEntries()}, nil
}

// listEntries returns all active relays sorted by name: explicit relays, per-user
// delegations, and the automatic socket-directory relays. It backs both the list
// control verb and the welcome page.
func (r *Relay) listEntries() []ListEntry {
	var entries []ListEntry
	r.relays.Range(func(name string, info relayInfo) bool {
		entries = append(entries, ListEntry{Name: name, Target: info.Target, Kind: KindRelay, CleanURL: info.CleanURL})
		return true
	})
	r.users.Range(func(name string, info relayInfo) bool {
		entries = append(entries, ListEntry{Name: name, Target: info.Target, Kind: KindUser, CleanURL: info.CleanURL})
		return true
	})
	entries = append(entries, r.socketEntries()...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Kind < entries[j].Kind
	})
	alive := r.alive.Load()
	for i := range entries {
		entries[i].Status = StatusUnknown
		if alive != nil {
			if up, ok := (*alive)[entries[i].Target]; ok {
				if up {
					entries[i].Status = StatusUp
				} else {
					entries[i].Status = StatusDown
				}
			}
		}
	}
	return entries
}

// socketEntries lists the automatic relays for <name>.sock files in the sockets
// directory, skipping the control socket and any name shadowed by an explicit
// relay (which takes precedence in routing).
func (r *Relay) socketEntries() []ListEntry {
	des, err := os.ReadDir(r.opts.SocketsDir)
	if err != nil {
		return nil
	}
	var entries []ListEntry
	for _, de := range des {
		name := strings.TrimSuffix(de.Name(), ".sock")
		if name == de.Name() || name == ReservedName {
			continue
		}
		if _, ok := r.relays.Load(name); ok {
			continue
		}
		path := filepath.Join(r.opts.SocketsDir, de.Name())
		fi, err := os.Stat(path)
		if err != nil || fi.Mode()&fs.ModeSocket == 0 {
			continue
		}
		entries = append(entries, ListEntry{Name: name, Target: path, Kind: KindSocket})
	}
	return entries
}

// Health-check cadence and per-target connect timeout.
const (
	healthCheckInterval = 15 * time.Second
	healthCheckTimeout  = 2 * time.Second
)

// startHealthChecks runs periodic liveness probes of all relay targets until the
// daemon is closed, refreshing the status shown on the welcome page and list.
func (r *Relay) startHealthChecks() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		r.checkHealth()
		t := time.NewTicker(healthCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-r.lifeCtx.Done():
				return
			case <-t.C:
				r.checkHealth()
			}
		}
	}()
}

// checkHealth probes every unique target once (concurrently) with a short connect
// timeout and stores the results for listEntries to report.
func (r *Relay) checkHealth() {
	// Collect unique targets and how to dial each.
	type dial struct{ network, address string }
	targets := make(map[string]dial)
	for _, e := range r.listEntries() {
		if _, seen := targets[e.Target]; seen {
			continue
		}
		if network, address, ok := entryDial(e); ok {
			targets[e.Target] = dial{network, address}
		} else {
			targets[e.Target] = dial{} // unresolvable -> down
		}
	}

	status := make(map[string]bool, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for target, d := range targets {
		wg.Add(1)
		go func(target string, d dial) {
			defer wg.Done()
			alive := false
			if d.network != "" {
				if conn, err := net.DialTimeout(d.network, d.address, healthCheckTimeout); err == nil {
					conn.Close()
					alive = true
				}
			}
			mu.Lock()
			status[target] = alive
			mu.Unlock()
		}(target, d)
	}
	wg.Wait()
	r.alive.Store(&status)
}

// entryDial returns the network and address to connect to for a liveness probe of
// a list entry.
func entryDial(e ListEntry) (network, address string, ok bool) {
	switch e.Kind {
	case KindRelay:
		network, address, _, err := relayDial(e.Target)
		if err != nil {
			return "", "", false
		}
		return network, address, true
	case KindUser, KindSocket:
		return "unix", e.Target, true
	default:
		return "", "", false
	}
}

// checkSocketOwner verifies socketPath is a Unix socket owned by username. A
// missing/wrong-type/wrong-owner socket wraps os.ErrNotExist so the caller can
// answer 404 without disclosing which condition failed.
func checkSocketOwner(socketPath, username string) error {
	usr, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("could not look up user %q: %w", username, err)
	}
	uid, err := strconv.Atoi(usr.Uid)
	if err != nil {
		return fmt.Errorf("invalid uid %q for user %q: %w", usr.Uid, username, err)
	}
	fi, err := os.Stat(socketPath)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("target %q is not a unix socket: %w", socketPath, os.ErrNotExist)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("could not determine ownership of %q", socketPath)
	}
	if int(st.Uid) != uid {
		return fmt.Errorf("socket %q is not owned by user %q: %w", socketPath, username, os.ErrNotExist)
	}
	return nil
}

// cleanHost returns the literal backend host used when a relay has clean-url set:
// "unix" for a unix-socket target, "127.0.0.1" for a tcp target.
func cleanHost(network string) string {
	if network == "unix" {
		return "unix"
	}
	return "127.0.0.1"
}

// userBackendHost strips the trailing user label so the delegated backend sees
// the same host the single-relay path uses: e.g. "app.alice.localhost" ->
// "app.localhost". The apex "alice.localhost" (no sub-label) maps to bare
// "localhost", so the user's own daemon serves its root/welcome page.
func userBackendHost(inHost string) string {
	host := inHost
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	rest := strings.TrimSuffix(host, ".localhost")
	if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		return rest[:i] + ".localhost"
	}
	return "localhost"
}

// newUserProxy builds a reverse proxy that forwards to the delegated user's Unix
// socket, resolving the socket path from the registry at dial time.
func (r *Relay) newUserProxy(username string) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Forward the original client host/scheme so the delegated daemon can
			// build correct links (e.g. its welcome page uses <name>.<user>.localhost
			// on the root's scheme and port, not its own bare <name>.localhost).
			pr.SetXForwarded()
			// User targets are always unix sockets, so clean-url forwards "unix".
			backendHost := userBackendHost(pr.In.Host)
			if info, ok := r.users.Load(username); ok && info.CleanURL {
				backendHost = cleanHost("unix")
			}
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = backendHost
			// ReverseProxy sends Out.Host (cloned from the inbound request) in
			// preference to Out.URL.Host, so the rewritten host must be set here.
			pr.Out.Host = backendHost
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				info, ok := r.users.Load(username)
				if !ok {
					return nil, fmt.Errorf("no delegation for user %q", username)
				}
				return (&net.Dialer{}).DialContext(ctx, "unix", info.Target)
			},
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}

// serveUser routes a <*>.<username>.localhost request to the user's delegated
// socket, verifying ownership on every request.
func (r *Relay) serveUser(w http.ResponseWriter, req *http.Request, username string) error {
	info, ok := r.users.Load(username)
	if !ok {
		return os.ErrNotExist
	}
	if err := checkSocketOwner(info.Target, username); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return err
	}
	handler, ok := r.userProxyMap.Load(username)
	if !ok {
		handler, _ = r.userProxyMap.LoadOrStore(username, r.newUserProxy(username))
	}
	handler.ServeHTTP(w, req)
	return nil
}

func (r *Relay) Close() error {
	r.lifeCancel(os.ErrClosed)
	r.wg.Wait()
	return nil
}

func (r *Relay) Start(ctx context.Context) (status error) {
	r.startStopMutex.Lock()
	defer r.startStopMutex.Unlock()

	s1, err := r.startLocked(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if status != nil {
			s1.Close()
		}
	}()

	var s2 *http.Server
	if tls := r.opts.TLSConfig(); tls != nil {
		s2, err = r.startLocked(ctx, tls)
		if err != nil {
			return err
		}
		defer func() {
			if status != nil {
				s2.Close()
			}
		}()
	}

	sc, err := r.startControlLocked(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if status != nil {
			sc.Close()
		}
	}()

	sf, err := r.startForwardLocked(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if status != nil && sf != nil {
			sf.Close()
		}
	}()

	r.httpServer = s1
	r.httpsServer = s2
	r.controlServer = sc
	r.forwardServer = sf

	r.startHealthChecks()
	return nil
}

// forwardSocketPath returns the Unix socket path a non-root instance exposes for
// root delegation, or "" for a root instance (which has no such socket).
func forwardSocketPath() string {
	uid := os.Getuid()
	if uid == 0 {
		return ""
	}
	return filepath.Join(fmt.Sprintf("/run/user/%d", uid), "localrelay.sock")
}

// startForwardLocked exposes the routing handler over the Unix socket at
// /run/user/<uid>/localrelay.sock so a root instance can forward this user's
// <*>.<user>.localhost subtree to it. It is best-effort: root instances have no
// such socket, and a missing runtime directory or an instance already listening
// there is logged as a warning rather than failing startup.
func (r *Relay) startForwardLocked(ctx context.Context) (*http.Server, error) {
	path := forwardSocketPath()
	if path == "" {
		return nil, nil // the root instance is the forwarder, not a target
	}
	runtimeDir := filepath.Dir(path)
	if fi, err := os.Stat(runtimeDir); err != nil || !fi.IsDir() {
		slog.Warn("user runtime directory is absent; not exposing the forward socket", "dir", runtimeDir)
		return nil, nil
	}
	// If another instance is actively listening, leave its socket alone.
	if conn, err := net.DialTimeout("unix", path, 500*time.Millisecond); err == nil {
		conn.Close()
		slog.Warn("another localrelay instance is already listening on the forward socket", "path", path)
		return nil, nil
	}
	// Any socket file here is stale (no live listener): remove and re-create it.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not remove stale forward socket; not exposing it", "path", path, "err", err)
		return nil, nil
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		slog.Warn("could not listen on the forward socket; not exposing it", "path", path, "err", err)
		return nil, nil
	}
	// 0600 keeps it owner-only; root (the forwarder) bypasses the permission check.
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		slog.Warn("could not set forward socket permissions; not exposing it", "path", path, "err", err)
		return nil, nil
	}

	server := &http.Server{
		Handler:     http.HandlerFunc(r.serveHTTP),
		BaseContext: func(net.Listener) context.Context { return r.lifeCtx },
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		if err := server.Serve(listener); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				slog.Error("forward server is closed unexpectedly", "err", err)
			}
		}
	}()
	slog.Info("exposing forward socket for root delegation", "path", path)
	return server, nil
}

// startControlLocked serves the control channel over the owner-only Unix socket
// <sockets-dir>/control.sock.
func (r *Relay) startControlLocked(ctx context.Context) (*http.Server, error) {
	if err := os.Remove(r.controlPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", r.controlPath)
	if err != nil {
		return nil, fmt.Errorf("could not open control socket %v: %w", r.controlPath, err)
	}
	if err := os.Chmod(r.controlPath, 0o600); err != nil {
		listener.Close()
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle(AddPath, httphelp.PostHandler(r.handleAdd))
	mux.Handle(RemovePath, httphelp.PostHandler(r.handleRemove))
	mux.Handle(ListPath, httphelp.PostHandler(r.handleList))
	server := &http.Server{
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return r.lifeCtx },
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		if err := server.Serve(listener); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				slog.Error("control server is closed unexpectedly", "err", err)
			}
		}
	}()

	return server, nil
}

func (r *Relay) startLocked(ctx context.Context, tlsCfg *tls.Config) (*http.Server, error) {
	address := net.JoinHostPort("localhost", strconv.Itoa(r.opts.HTTPPort))
	if tlsCfg != nil {
		address = net.JoinHostPort("localhost", strconv.Itoa(r.opts.HTTPSPort))
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("could not open listener at %v: %w", address, err)
	}
	if tlsCfg != nil {
		listener = tls.NewListener(listener, tlsCfg)
	}

	server := &http.Server{
		Handler:     http.HandlerFunc(r.serveHTTP),
		BaseContext: func(net.Listener) context.Context { return r.lifeCtx },
		TLSConfig:   tlsCfg,
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		if err := server.Serve(listener); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				slog.Error("http server is closed unexpectedly", "err", err)
			}
		}
	}()

	return server, nil
}

func (r *Relay) Stop() error {
	r.startStopMutex.Lock()
	defer r.startStopMutex.Unlock()

	if r.httpServer != nil {
		r.httpServer.Close()
		r.httpServer = nil
	}
	if r.httpsServer != nil {
		r.httpsServer.Close()
		r.httpsServer = nil
	}
	if r.controlServer != nil {
		r.controlServer.Close()
		r.controlServer = nil
	}
	if r.forwardServer != nil {
		r.forwardServer.Close()
		r.forwardServer = nil
	}
	return nil
}

func (r *Relay) serveHTTP(w http.ResponseWriter, req *http.Request) {
	err := r.handleRequest(w, req)
	switch {
	case err == nil:
		return
	case errors.Is(err, os.ErrNotExist):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, os.ErrInvalid):
		w.WriteHeader(http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (r *Relay) handleRequest(w http.ResponseWriter, req *http.Request) error {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	// The apex localhost host serves a welcome page listing the relays instead of
	// routing to a backend.
	if host == "localhost" {
		return r.serveWelcome(w, req)
	}
	if !strings.HasSuffix(host, ".localhost") {
		return os.ErrNotExist
	}
	rest := strings.TrimSuffix(host, ".localhost")
	if rest == "" {
		return os.ErrNotExist
	}
	// A multi-label host <*>.<user>.localhost is a per-user delegation; the last
	// label is the username and the rest is the wildcard subdomain.
	if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		sub, username := rest[:i], rest[i+1:]
		if sub == "" || username == "" {
			return os.ErrNotExist
		}
		return r.serveUser(w, req, username)
	}
	target := rest
	// The reserved name is never routable; it also names the control socket.
	if target == ReservedName {
		return os.ErrNotExist
	}
	if handler, ok := r.targetMap.Load(target); ok {
		handler.ServeHTTP(w, req)
		return nil
	}
	// An explicit relay takes precedence over a conventional socket.
	if _, ok := r.relays.Load(target); ok {
		handler, _ := r.targetMap.LoadOrStore(target, r.newRelayProxy(target))
		handler.ServeHTTP(w, req)
		return nil
	}
	// Check if sockets-dir has an entry for the target.
	socketPath := filepath.Join(r.opts.SocketsDir, target+".sock")
	if s, err := os.Stat(socketPath); err == nil && s.Mode()&fs.ModeSocket != 0 {
		handler, _ := r.targetMap.LoadOrStore(target, newSocketProxy(target, socketPath))
		handler.ServeHTTP(w, req)
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not stat target socket: %w", err)
	}
	// The apex of a per-user delegation: <user>.localhost forwards to the user's
	// socket as Host: localhost, so the user's own daemon serves its root page.
	if _, ok := r.users.Load(target); ok {
		return r.serveUser(w, req, target)
	}
	return os.ErrNotExist
}

// newSocketProxy builds the automatic relay for a conventional <name>.sock in the
// sockets directory: a plaintext http hop over the fixed unix socket.
func newSocketProxy(name, socketPath string) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The DialContext below ignores the URL host, so any non-empty host
			// works; the scheme must be http for the plaintext unix hop.
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = name + ".localhost"
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}
}
