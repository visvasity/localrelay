// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/visvasity/cli"
	"github.com/visvasity/localrelay/servers"
	"github.com/visvasity/opensslcmd"
	"github.com/visvasity/shcmd"
)

// CACertCmd mints a leaf serverAuth certificate for an FQDN under the localhost
// TLD (e.g. foo.localhost, foo.bar.localhost), signed by the CA in -ca-dir. It
// writes <fqdn>.crt and <fqdn>.key into the current directory. With -wildcard the
// certificate instead covers *.<fqdn> and is written as wildcard.<fqdn>.{crt,key}.
type CACertCmd struct {
	caDir    string
	curve    string
	days     int
	wildcard bool
}

func (c *CACertCmd) Purpose() string {
	return "Create a leaf serverAuth certificate for a localhost FQDN"
}

func (c *CACertCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	fset.StringVar(&c.caDir, "ca-dir", "", "Directory holding the signing CA (key.pem, cert.pem)")
	fset.StringVar(&c.curve, "curve", "prime256v1", "EC curve for the leaf key")
	fset.IntVar(&c.days, "days", 3650, "Validity period of the leaf certificate in days")
	fset.BoolVar(&c.wildcard, "wildcard", false, "Certify *.<fqdn> instead of <fqdn> (e.g. foo.localhost -> *.foo.localhost)")
	return "cert", fset, c.run
}

func (c *CACertCmd) run(ctx context.Context, args []string) (retErr error) {
	if len(args) != 1 {
		return errors.New("usage: ca cert -ca-dir <dir> <fqdn>")
	}
	fqdn := args[0]
	if c.caDir == "" {
		return errors.New("the -ca-dir directory is required")
	}
	if err := validLocalhostFQDN(fqdn); err != nil {
		return err
	}

	// name is the DNS name to certify; base is the output filename stem. A "*."
	// prefix is shell-hostile, so wildcard files use a "wildcard." stem instead.
	name, base := fqdn, fqdn
	if c.wildcard {
		name = "*." + fqdn
		base = "wildcard." + fqdn
	}
	keyPath := base + ".key"
	certPath := base + ".crt"
	for _, p := range []string{keyPath, certPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s already exists", p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// Leave no half-written key/cert behind if a later step fails.
	defer func() {
		if retErr != nil {
			os.Remove(keyPath)
			os.Remove(certPath)
		}
	}()

	r := opensslcmd.Runner{Runner: shcmd.Runtime()}
	if err := r.GenerateECKey(ctx, keyPath, c.curve); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "localrelay-cert-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	csrPath := filepath.Join(tmpDir, "leaf.csr")
	subj := &opensslcmd.Subject{CommonName: name}
	san := &opensslcmd.SAN{DNS: []string{name}}
	if err := r.CreateCSR(ctx, keyPath, csrPath, subj, san); err != nil {
		return err
	}
	if err := r.SignServerCert(ctx, c.caDir, csrPath, certPath, c.days, san); err != nil {
		return err
	}
	fmt.Fprintf(cli.Stdout(ctx), "wrote %s and %s\n", certPath, keyPath)
	return nil
}

// validLocalhostFQDN checks that fqdn is either the bare localhost TLD or a name
// under it (e.g. foo.localhost, foo.bar.localhost), each label being a valid DNS
// label.
func validLocalhostFQDN(fqdn string) error {
	if fqdn == "localhost" {
		return nil
	}
	if !strings.HasSuffix(fqdn, ".localhost") {
		return fmt.Errorf("%q must be localhost or a name under the localhost TLD (e.g. foo.localhost)", fqdn)
	}
	labels := strings.Split(fqdn, ".")
	prefix := labels[:len(labels)-1] // drop the trailing "localhost" label
	if len(prefix) == 0 {
		return fmt.Errorf("%q must have at least one label before .localhost", fqdn)
	}
	for _, label := range prefix {
		if err := servers.ValidName(label); err != nil {
			return fmt.Errorf("invalid label in %q: %w", fqdn, err)
		}
	}
	return nil
}
