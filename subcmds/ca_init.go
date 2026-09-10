// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/visvasity/cli"
	"github.com/visvasity/opensslcmd"
	"github.com/visvasity/shcmd"
)

// CAInitCmd creates a name-constrained localhost root CA (key.pem and cert.pem)
// in the target directory, for terminating HTTPS at the daemon (§13). Only a
// root CA is created; intermediate CAs are not supported for the localhost domain.
type CAInitCmd struct {
	curve string
	days  int
	cn    string
}

func (c *CAInitCmd) Purpose() string {
	return "Create a name-constrained localhost root CA"
}

func (c *CAInitCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	fset.StringVar(&c.curve, "curve", "prime256v1", "EC curve for the CA key")
	fset.IntVar(&c.days, "days", 3650, "Validity period of the CA certificate in days")
	fset.StringVar(&c.cn, "cn", "localrelay Local CA", "Common Name of the CA certificate subject")
	return "init", fset, c.run
}

func (c *CAInitCmd) run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ca init <dir>")
	}
	dir := args[0]

	// The CA certificate is public; only key.pem is secret (kept at mode 0600). So
	// the directory and the cert are world-readable, letting any user install from
	// the same <ca-dir> — including a CA created by root. Parents created here are
	// traversable for the same reason.
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}

	r := opensslcmd.Runner{Runner: shcmd.Runtime()}
	subj := &opensslcmd.Subject{CommonName: c.cn}
	if err := r.CreateECLocalhostCA(ctx, dir, subj, c.curve, c.days); err != nil {
		return err
	}
	// CreateECLocalhostCA makes the directory 0700; open traversal so non-root
	// users can reach the public cert (key.pem stays 0600).
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(dir, "cert.pem"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(cli.Stdout(ctx), "wrote name-constrained root CA to %s (key.pem, cert.pem)\n", dir)
	return nil
}
