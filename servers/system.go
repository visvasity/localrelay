// Copyright (c) 2026 Visvasity LLC

package servers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/visvasity/syncmap"
)

// System is the localrelay system-mode daemon. It routes a three-label host
// <name>.<user>.localhost to the delegated user's per-user daemon over the
// user's Unix socket <sockets-dir>/<user>.sock, stripping the user label and
// rewriting the forwarded Host to <name>.localhost (SPEC §12). A two-label or
// otherwise malformed host is not routable and yields 404.
//
// NOTE: the connect-time target-ownership check (SPEC §12.5 — the target socket
// must be owned by the delegated user) is not yet implemented; this mirrors the
// presence-based routing in user.go and can be added next.
type System struct {
	lifeCtx    context.Context
	lifeCancel context.CancelCauseFunc

	wg sync.WaitGroup

	opts *Options

	dataDir string

	startStopMutex sync.Mutex
	httpServer     *http.Server
	httpsServer    *http.Server

	targetMap syncmap.Map[string, http.Handler]
}

func NewSystem(dataDir string, opts *Options) (_ *System, err error) {
	if opts == nil {
		opts = new(Options)
	}
	opts.setDefaults()
	if err := opts.Check(context.TODO()); err != nil {
		return nil, err
	}
	lifeCtx, lifeCancel := context.WithCancelCause(context.Background())
	s := &System{
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
		opts:       opts,
		dataDir:    dataDir,
	}
	return s, nil
}

func (s *System) Close() error {
	s.lifeCancel(os.ErrClosed)
	s.wg.Wait()
	return nil
}

func (s *System) Start(ctx context.Context) (status error) {
	s.startStopMutex.Lock()
	defer s.startStopMutex.Unlock()

	s1, err := s.startLocked(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if status != nil {
			s1.Close()
		}
	}()

	var s2 *http.Server
	if tls := s.opts.TLSConfig(); tls != nil {
		s2, err = s.startLocked(ctx, tls)
		if err != nil {
			return err
		}
		defer func() {
			if status != nil {
				s2.Close()
			}
		}()
	}

	s.httpServer = s1
	s.httpsServer = s2
	return nil
}

func (s *System) startLocked(ctx context.Context, tlsCfg *tls.Config) (*http.Server, error) {
	address := net.JoinHostPort("localhost", strconv.Itoa(s.opts.HTTPPort))
	if tlsCfg != nil {
		address = net.JoinHostPort("localhost", strconv.Itoa(s.opts.HTTPSPort))
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("could not open listener at %v: %w", address, err)
	}
	if tlsCfg != nil {
		listener = tls.NewListener(listener, tlsCfg)
	}

	server := &http.Server{
		Handler:     http.HandlerFunc(s.serveHTTP),
		BaseContext: func(net.Listener) context.Context { return s.lifeCtx },
		TLSConfig:   tlsCfg,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		if err := server.Serve(listener); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				slog.Error("http server is closed unexpectedly", "err", err)
			}
		}
	}()

	return server, nil
}

func (s *System) Stop() error {
	s.startStopMutex.Lock()
	defer s.startStopMutex.Unlock()

	if s.httpServer != nil {
		s.httpServer.Close()
		s.httpServer = nil
	}
	if s.httpsServer != nil {
		s.httpsServer.Close()
		s.httpsServer = nil
	}
	return nil
}

func (s *System) serveHTTP(w http.ResponseWriter, r *http.Request) {
	err := s.handleRequest(w, r)
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

func (s *System) handleRequest(w http.ResponseWriter, r *http.Request) error {
	_, user, ok := splitDelegatedHost(r.Host)
	if !ok {
		return os.ErrNotExist
	}
	if handler, ok := s.targetMap.Load(user); ok {
		handler.ServeHTTP(w, r)
		return nil
	}
	// Check if sockets-dir has an entry for the delegated user.
	socketPath := filepath.Join(s.opts.SocketsDir, user+".sock")
	if st, err := os.Stat(socketPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not stat delegation socket: %w", err)
		}
		return err
	} else if st.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("delegation target is not a unix domain socket: %w", os.ErrNotExist)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Strip the user label: forward <name>.<user>.localhost as
			// <name>.localhost. The name is recomputed from each request so one
			// cached proxy per user serves every service name.
			name, _, ok := splitDelegatedHost(pr.In.Host)
			if !ok {
				name = "" // unreachable: handleRequest already validated the host
			}
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = name + ".localhost"
			pr.Out.Host = name + ".localhost"
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
	handler, _ := s.targetMap.LoadOrStore(user, proxy)
	handler.ServeHTTP(w, r)
	return nil
}

// splitDelegatedHost parses a three-label host <name>.<user>.localhost, returning
// the service name and user labels. Hosts with any other label count (including
// the two-label per-user form) do not match.
func splitDelegatedHost(host string) (name, user string, ok bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	if !strings.HasSuffix(host, ".localhost") {
		return "", "", false
	}
	rest := strings.TrimSuffix(host, ".localhost")
	i := strings.IndexByte(rest, '.')
	if i <= 0 {
		return "", "", false // fewer than three labels
	}
	name, user = rest[:i], rest[i+1:]
	if user == "" || strings.ContainsRune(user, '.') {
		return "", "", false // more than three labels
	}
	return name, user, true
}
