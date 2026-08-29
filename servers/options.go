// Copyright (c) 2026 Visvasity LLC

package servers

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

type Options struct {
	System bool

	SocketsDir string

	TLSCertPath string
	TLSKeyPath  string

	HTTPPort  int
	HTTPSPort int

	tlsConfig *tls.Config
}

func (v *Options) SetFlags(fset *flag.FlagSet, defaults *Options) {
	if defaults == nil {
		defaults = v
	}
	// Ports register with the caller's default (0 by default). They are resolved
	// in setDefaults, which depends on -system and so cannot be known until after
	// parsing.
	fset.BoolVar(&v.System, "system", defaults.System, "Run as the privileged system delegation daemon")
	fset.IntVar(&v.HTTPPort, "http-port", defaults.HTTPPort, "TCP port for the HTTP endpoint (default 1080; 80 with -system)")
	fset.IntVar(&v.HTTPSPort, "https-port", defaults.HTTPSPort, "TCP port for the HTTPS endpoint (default 1443; 443 with -system)")
	fset.StringVar(&v.TLSCertPath, "tls-cert", defaults.TLSCertPath, "Path to the TLS certificate file")
	fset.StringVar(&v.TLSKeyPath, "tls-key", defaults.TLSKeyPath, "Path to the TLS private key file")
	fset.StringVar(&v.SocketsDir, "sockets-dir", defaults.SocketsDir, "Path to the reverse proxy target sockets directory")
}

func (v *Options) setDefaults() {
	if v.HTTPPort == 0 {
		if v.System {
			v.HTTPPort = 80
		} else {
			v.HTTPPort = 1080
		}
	}
	if v.HTTPSPort == 0 {
		if v.System {
			v.HTTPSPort = 443
		} else {
			v.HTTPSPort = 1443
		}
	}
	if len(v.SocketsDir) == 0 {
		if d := os.Getenv("XDB_RUNTIME_DIR"); len(d) != 0 {
			v.SocketsDir = filepath.Join(d, "localrelay")
		}
	}
}

func (v *Options) Check(context.Context) error {
	if len(v.SocketsDir) == 0 {
		return fmt.Errorf("sockets directory (-sockets-dir) path cannot be empty")
	}
	if v.HTTPPort <= 0 {
		return fmt.Errorf("http port (-http-port) cannot be zero or -ve")
	}
	if v.HTTPSPort <= 0 {
		return fmt.Errorf("https port (-https-port) cannot be zero or -ve")
	}
	if (len(v.TLSCertPath) == 0) != (len(v.TLSKeyPath) == 0) {
		return fmt.Errorf("both TLS certificate (-tls-cert) and key (-tls-key) are required to enable HTTPS")
	}
	if len(v.TLSCertPath) != 0 {
		cert, err := tls.LoadX509KeyPair(v.TLSCertPath, v.TLSKeyPath)
		if err != nil {
			return fmt.Errorf("could not load TLS keypair: %w", err)
		}
		v.tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	return nil
}

func (v *Options) TLSConfig() *tls.Config {
	return v.tlsConfig
}
