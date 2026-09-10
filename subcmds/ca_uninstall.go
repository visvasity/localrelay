// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/visvasity/cli"
)

// CAUninstallCmd removes a previously-installed localhost CA from a trust store.
// It mirrors ca install: the same -firefox/-chrome/-system flags select the same
// targets, and each removal is the exact inverse of the corresponding install.
// Before removing anything it verifies the certificate to be removed is a CA
// name-constrained to "localhost", so it never removes an unrelated certificate.
type CAUninstallCmd struct {
	firefox bool
	chrome  bool
	system  bool
}

func (c *CAUninstallCmd) Purpose() string {
	return "Remove a localhost CA from the current user's browser or system trust store"
}

func (c *CAUninstallCmd) Command() (string, *flag.FlagSet, cli.CmdFunc) {
	fset := new(flag.FlagSet)
	fset.BoolVar(&c.firefox, "firefox", false, "Remove from the current user's Firefox profiles")
	fset.BoolVar(&c.chrome, "chrome", false, "Remove from the current user's Chrome/Chromium NSS store")
	fset.BoolVar(&c.system, "system", false, "Remove from the host-wide system trust store (requires root)")
	return "uninstall", fset, c.run
}

func (c *CAUninstallCmd) run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ca uninstall [-firefox] [-chrome] [-system] <ca-dir>")
	}
	certPath := filepath.Join(args[0], "cert.pem")
	data, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("no CA certificate at %s: %w", certPath, err)
	}
	// Refuse to proceed unless the reference cert is a name-constrained localhost CA.
	if _, err := localhostCACert(data); err != nil {
		return fmt.Errorf("%s: %w", certPath, err)
	}
	nickname := caNickname(certPath)
	out := cli.Stdout(ctx)

	// Mirror install's target selection exactly.
	explicit := c.firefox || c.chrome || c.system
	doFirefox := c.firefox || (!explicit && firefoxPresent())
	doChrome := c.chrome || (!explicit && chromePresent())
	if !c.system && !doFirefox && !doChrome {
		return errors.New("no browser trust store found for the current user; use -firefox, -chrome, or -system")
	}

	if c.system {
		loc, ok, err := uninstallSystemCA(ctx, certPath)
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(out, "removed CA from the system trust store (%s)\n", loc)
		} else {
			fmt.Fprintf(out, "system trust store: no localrelay CA to remove\n")
		}
	}
	if doFirefox {
		n, err := uninstallFirefoxCA(ctx, nickname)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "removed CA from %d Firefox profile(s)\n", n)
	}
	if doChrome {
		n, err := uninstallChromeCA(ctx, nickname)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "removed CA from %d Chrome/Chromium NSS store(s)\n", n)
	}
	return nil
}

// uninstallChromeCA removes the CA from every initialized Chrome/Chromium NSS
// store, returning the number of stores it was removed from.
func uninstallChromeCA(ctx context.Context, nickname string) (int, error) {
	certutil, err := certutilPath()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, d := range presentChromeNSSDirs() {
		ok, err := removeLocalhostCAFromNSSDB(ctx, certutil, "sql:"+d, nickname)
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

// uninstallFirefoxCA removes the CA from every initialized Firefox profile,
// returning the number of profiles it was removed from.
func uninstallFirefoxCA(ctx context.Context, nickname string) (int, error) {
	certutil, err := certutilPath()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, p := range firefoxProfiles() {
		dbSpec := nssProfileDB(p)
		if dbSpec == "" {
			continue
		}
		ok, err := removeLocalhostCAFromNSSDB(ctx, certutil, dbSpec, nickname)
		if err != nil {
			return removed, fmt.Errorf("Firefox profile %s: %w", p, err)
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

// removeLocalhostCAFromNSSDB deletes the certificate stored under nickname from
// dbSpec, but only after dumping it and confirming it is a CA name-constrained to
// localhost — so it never removes an unrelated cert sharing the nickname. A
// missing nickname is a no-op (returns false, nil).
func removeLocalhostCAFromNSSDB(ctx context.Context, certutil, dbSpec, nickname string) (bool, error) {
	pemOut, err := exec.CommandContext(ctx, certutil, "-L", "-d", dbSpec, "-n", nickname, "-a").Output()
	if err != nil {
		return false, nil // nickname not present in this database
	}
	if _, err := localhostCACert(pemOut); err != nil {
		return false, fmt.Errorf("refusing to remove %q from %s: %w", nickname, dbSpec, err)
	}
	if out, err := exec.CommandContext(ctx, certutil, "-D", "-d", dbSpec, "-n", nickname).CombinedOutput(); err != nil {
		return false, fmt.Errorf("certutil -D on %s: %w: %s", dbSpec, err, strings.TrimSpace(string(out)))
	}
	return true, nil
}
