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

	"github.com/visvasity/httphelp"
	"github.com/visvasity/syncmap"
)

type User struct {
	lifeCtx    context.Context
	lifeCancel context.CancelCauseFunc

	wg sync.WaitGroup

	opts *Options

	dataDir string

	startStopMutex sync.Mutex
	httpServer     *http.Server
	httpsServer    *http.Server

	rproxy    *httphelp.Server
	targetMap syncmap.Map[string, http.Handler]
}

func New(dataDir string, opts *Options) (_ *User, err error) {
	if opts == nil {
		opts = new(Options)
	}
	opts.setDefaults()
	if err := opts.Check(context.TODO()); err != nil {
		return nil, err
	}
	lifeCtx, lifeCancel := context.WithCancelCause(context.Background())
	u := &User{
		lifeCtx:    lifeCtx,
		lifeCancel: lifeCancel,
		opts:       opts,
		dataDir:    dataDir,
	}
	return u, nil
}

func (u *User) Close() error {
	u.lifeCancel(os.ErrClosed)
	u.wg.Wait()
	return nil
}

func (u *User) Start(ctx context.Context) (status error) {
	u.startStopMutex.Lock()
	defer u.startStopMutex.Unlock()

	s1, err := u.startLocked(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if status != nil {
			s1.Close()
		}
	}()

	var s2 *http.Server
	if tls := u.opts.TLSConfig(); tls != nil {
		s2, err = u.startLocked(ctx, tls)
		if err != nil {
			return err
		}
		defer func() {
			if status != nil {
				s2.Close()
			}
		}()
	}

	u.httpServer = s1
	u.httpsServer = s2
	return nil
}

func (u *User) startLocked(ctx context.Context, tlsCfg *tls.Config) (*http.Server, error) {
	address := net.JoinHostPort("localhost", strconv.Itoa(u.opts.HTTPPort))
	if tlsCfg != nil {
		address = net.JoinHostPort("localhost", strconv.Itoa(u.opts.HTTPSPort))
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("could not open listener at %v: %w", address, err)
	}
	if tlsCfg != nil {
		listener = tls.NewListener(listener, tlsCfg)
	}

	server := &http.Server{
		Handler:     http.HandlerFunc(u.serveHTTP),
		BaseContext: func(net.Listener) context.Context { return u.lifeCtx },
		TLSConfig:   tlsCfg,
	}

	u.wg.Add(1)
	go func() {
		defer u.wg.Done()

		if err := server.Serve(listener); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				slog.Error("http server is closed unexpectedly", "err", err)
			}
		}
	}()

	return server, nil
}

func (u *User) Stop() error {
	u.startStopMutex.Lock()
	defer u.startStopMutex.Unlock()

	if u.httpServer != nil {
		u.httpServer.Close()
		u.httpServer = nil
	}
	if u.httpsServer != nil {
		u.httpsServer.Close()
		u.httpsServer = nil
	}
	return nil
}

func (u *User) serveHTTP(w http.ResponseWriter, r *http.Request) {
	err := u.handleRequest(w, r)
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

func (u *User) handleRequest(w http.ResponseWriter, r *http.Request) error {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	if !strings.HasSuffix(host, ".localhost") {
		return os.ErrNotExist
	}
	target := strings.TrimSuffix(host, ".localhost")
	if target == "" || strings.ContainsRune(target, '.') {
		return os.ErrNotExist
	}
	if handler, ok := u.targetMap.Load(target); ok {
		handler.ServeHTTP(w, r)
		return nil
	}
	// Check if sockets-dir has an entry for the target.
	socketPath := filepath.Join(u.opts.SocketsDir, target+".sock")
	if s, err := os.Stat(socketPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("could not stat target socket: %w", err)
		}
		return err
	} else if s.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("target is not a unix domain socket: %w", os.ErrNotExist)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The DialContext below ignores the URL host, so any non-empty host
			// works; the scheme must be http for the plaintext unix hop.
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = target + ".localhost"
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
	handler, _ := u.targetMap.LoadOrStore(target, proxy)
	handler.ServeHTTP(w, r)
	return nil
}
