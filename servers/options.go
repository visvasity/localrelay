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
	SocketsDir string

	// CADir, when set, points at a name-constrained localhost CA (cert.pem and
	// key.pem) that the daemon uses to mint per-host TLS certificates on the fly,
	// enabling HTTPS for every name under the localhost domain.
	CADir string

	HTTPPort  int
	HTTPSPort int

	tlsConfig *tls.Config
}

func (v *Options) SetFlags(fset *flag.FlagSet, defaults *Options) {
	if defaults == nil {
		defaults = v
	}
	// Ports register with the caller's default (0 by default); setDefaults then
	// resolves 0 from the effective UID (root gets the privileged ports).
	fset.IntVar(&v.HTTPPort, "http-port", defaults.HTTPPort, "TCP port for the HTTP endpoint (default 1080; 80 as root)")
	fset.IntVar(&v.HTTPSPort, "https-port", defaults.HTTPSPort, "TCP port for the HTTPS endpoint (default 1443; 443 as root)")
	fset.StringVar(&v.CADir, "ca-dir", defaults.CADir, "Directory with a localhost CA (cert.pem, key.pem) for minting per-host TLS certs on the fly; enables HTTPS")
	fset.StringVar(&v.SocketsDir, "sockets-dir", defaults.SocketsDir, "Path to the reverse proxy target sockets directory")
}

func (v *Options) setDefaults() {
	if v.HTTPPort == 0 {
		if os.Getuid() == 0 {
			v.HTTPPort = 80
		} else {
			v.HTTPPort = 1080
		}
	}
	if v.HTTPSPort == 0 {
		if os.Getuid() == 0 {
			v.HTTPSPort = 443
		} else {
			v.HTTPSPort = 1443
		}
	}
	v.SocketsDir = ResolveSocketsDir(v.SocketsDir)
}

// ResolveSocketsDir returns dir if non-empty, otherwise the default sockets
// directory: /run/localrelay when running as root, else $HOME/.sockets. Sockets
// are runtime-scoped, so root uses /run (typically a tmpfs cleared on reboot)
// rather than the persistent data directory. The daemon and the CLI use it so
// both agree on where the control socket lives.
func ResolveSocketsDir(dir string) string {
	if dir != "" {
		return dir
	}
	if os.Getuid() == 0 {
		return "/run/localrelay"
	}
	return filepath.Join(os.Getenv("HOME"), ".sockets")
}

// ResolveDataDir returns dir if non-empty, otherwise the default data directory:
// /var/lib/localrelay when running as root, else $HOME/.localrelay.
func ResolveDataDir(dir string) string {
	if dir != "" {
		return dir
	}
	if os.Getuid() == 0 {
		return "/var/lib/localrelay"
	}
	return filepath.Join(os.Getenv("HOME"), ".localrelay")
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
	if len(v.CADir) != 0 {
		minter, err := newCertMinter(v.CADir)
		if err != nil {
			return fmt.Errorf("could not load CA from %s: %w", v.CADir, err)
		}
		v.tlsConfig = &tls.Config{
			GetCertificate: minter.getCertificate,
			MinVersion:     tls.VersionTLS12,
		}
	}
	return nil
}

func (v *Options) TLSConfig() *tls.Config {
	return v.tlsConfig
}
