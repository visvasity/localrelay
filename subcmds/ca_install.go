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
)

// CAInstallCmd installs an existing localhost CA (the cert.pem in <ca-dir>) into a
// trust store. By default it targets the current user's browser trust stores
// (Firefox and Chrome), which needs no root; -firefox/-chrome narrow that to one
// browser, and -system installs into the host-wide system store (requires root).
type CAInstallCmd struct {
	firefox bool
	chrome  bool
	system  bool
}

func (c *CAInstallCmd) Purpose() string {
	return "Install a localhost CA into the current user's browser or system trust store"
}

func (c *CAInstallCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	fset.BoolVar(&c.firefox, "firefox", false, "Install into the current user's Firefox profiles")
	fset.BoolVar(&c.chrome, "chrome", false, "Install into the current user's Chrome/Chromium NSS store")
	fset.BoolVar(&c.system, "system", false, "Install into the host-wide system trust store (requires root)")
	return "install", fset, c.run
}

func (c *CAInstallCmd) run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ca install [-firefox] [-chrome] [-system] <ca-dir>")
	}
	certPath := filepath.Join(args[0], "cert.pem")
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("no CA certificate at %s: %w", certPath, err)
	}
	out := cli.Stdout(ctx)
	nickname := caNickname(certPath)

	// With no target flags the default is the current user's browsers, but only
	// the ones actually present, so a missing browser is skipped rather than an
	// error. An explicit -firefox/-chrome forces that browser (creating its store
	// if needed).
	explicit := c.firefox || c.chrome || c.system
	doFirefox := c.firefox || (!explicit && firefoxPresent())
	doChrome := c.chrome || (!explicit && chromePresent())

	installed := false
	if c.system {
		loc, err := installSystemCA(ctx, certPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "installed CA into the system trust store (%s)\n", loc)
		installed = true
	}
	if doFirefox {
		n, err := installFirefoxCA(ctx, certPath, nickname)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "installed CA into %d Firefox profile(s)\n", n)
		installed = true
	}
	if doChrome {
		n, err := installChromeCA(ctx, certPath, nickname)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "installed CA into %d Chrome/Chromium NSS store(s)\n", n)
		installed = true
	}
	if !installed {
		return errors.New("no browser trust store found for the current user; use -firefox, -chrome, or -system")
	}
	return nil
}
