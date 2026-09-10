// Copyright (c) 2026 Visvasity LLC

//go:build linux

package subcmds

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linuxSystemTrust returns the system trust-anchor file path and the refresh
// command for this distribution, probing the well-known locations in the same
// order as mkcert (Fedora/RHEL, Debian/Ubuntu, Arch, openSUSE). ok is false when
// none is present.
func linuxSystemTrust() (anchorPath string, refresh []string, ok bool) {
	switch {
	case dirExists("/etc/pki/ca-trust/source/anchors/"):
		return "/etc/pki/ca-trust/source/anchors/localrelay.pem", []string{"update-ca-trust", "extract"}, true
	case dirExists("/usr/local/share/ca-certificates/"):
		return "/usr/local/share/ca-certificates/localrelay.crt", []string{"update-ca-certificates"}, true
	case dirExists("/etc/ca-certificates/trust-source/anchors/"):
		return "/etc/ca-certificates/trust-source/anchors/localrelay.crt", []string{"trust", "extract-compat"}, true
	case dirExists("/usr/share/pki/trust/anchors/"):
		return "/usr/share/pki/trust/anchors/localrelay.pem", []string{"update-ca-certificates"}, true
	}
	return "", nil, false
}

// installSystemCA drops the PEM cert at certPath into the distribution's trust
// anchor directory and refreshes the system store. Requires root. It returns the
// anchor path for reporting.
func installSystemCA(ctx context.Context, certPath string) (string, error) {
	if os.Geteuid() != 0 {
		return "", errSystemTrustRoot
	}
	anchor, refresh, ok := linuxSystemTrust()
	if !ok {
		return "", errors.New("no supported system trust store found on this system")
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(anchor), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(anchor, data, 0o644); err != nil {
		return "", err
	}
	if err := runTrustRefresh(ctx, refresh); err != nil {
		return "", err
	}
	return anchor, nil
}

// uninstallSystemCA removes the trust anchor file and refreshes the system store,
// but only after confirming the anchor is a name-constrained localhost CA.
// Requires root. certPath is unused on Linux (the anchor is at a fixed location).
func uninstallSystemCA(ctx context.Context, certPath string) (string, bool, error) {
	if os.Geteuid() != 0 {
		return "", false, errSystemTrustRoot
	}
	anchor, refresh, ok := linuxSystemTrust()
	if !ok {
		return "", false, errors.New("no supported system trust store found on this system")
	}
	data, err := os.ReadFile(anchor)
	if errors.Is(err, os.ErrNotExist) {
		return anchor, false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := localhostCACert(data); err != nil {
		return "", false, fmt.Errorf("refusing to remove %s: %w", anchor, err)
	}
	if err := os.Remove(anchor); err != nil {
		return "", false, err
	}
	if err := runTrustRefresh(ctx, refresh); err != nil {
		return "", false, err
	}
	return anchor, true, nil
}

// runTrustRefresh runs the distribution trust-refresh command, resolving its
// binary from PATH or the sbin directories that a non-login PATH often omits.
func runTrustRefresh(ctx context.Context, cmd []string) error {
	bin, err := resolveTrustTool(cmd[0])
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, bin, cmd[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(cmd, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveTrustTool(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, d := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin"} {
		if p := filepath.Join(d, name); fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%q not found; install your distribution's ca-certificates tools", name)
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
