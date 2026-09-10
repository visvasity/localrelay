// Copyright (c) 2026 Visvasity LLC

//go:build darwin

package subcmds

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// macSystemKeychain is the machine-wide keychain a trusted root is added to.
const macSystemKeychain = "/Library/Keychains/System.keychain"

// installSystemCA adds the CA to the macOS system keychain as a trusted root,
// using the security(1) tool. Requires root.
func installSystemCA(ctx context.Context, certPath string) (string, error) {
	if os.Geteuid() != 0 {
		return "", errSystemTrustRoot
	}
	out, err := exec.CommandContext(ctx, "security", "add-trusted-cert",
		"-d", "-r", "trustRoot", "-k", macSystemKeychain, certPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("security add-trusted-cert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return macSystemKeychain, nil
}

// uninstallSystemCA removes the CA from the macOS system keychain. The reference
// certificate in certPath (already validated as a name-constrained localhost CA
// by the caller) identifies exactly which cert to remove. Requires root.
func uninstallSystemCA(ctx context.Context, certPath string) (string, bool, error) {
	if os.Geteuid() != 0 {
		return "", false, errSystemTrustRoot
	}
	out, err := exec.CommandContext(ctx, "security", "remove-trusted-cert", "-d", certPath).CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("security remove-trusted-cert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return macSystemKeychain, true, nil
}
