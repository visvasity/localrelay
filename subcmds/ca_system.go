// Copyright (c) 2026 Visvasity LLC

package subcmds

import "errors"

// The per-OS system trust store is installed/removed by installSystemCA and
// uninstallSystemCA, defined per platform in ca_system_{linux,darwin,other}.go.

// errSystemTrustRoot is returned when a system-trust-store operation is attempted
// without root privileges.
var errSystemTrustRoot = errors.New("operating on the system trust store requires root; re-run with sudo")
