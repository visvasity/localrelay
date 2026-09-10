// Copyright (c) 2026 Visvasity LLC

//go:build !linux && !darwin

package subcmds

import (
	"context"
	"errors"
)

// installSystemCA is unsupported on platforms without a known system trust store
// backend; browser (-firefox/-chrome) installation still works there.
func installSystemCA(ctx context.Context, certPath string) (string, error) {
	return "", errors.New("system trust store installation is not supported on this platform")
}

func uninstallSystemCA(ctx context.Context, certPath string) (string, bool, error) {
	return "", false, errors.New("system trust store removal is not supported on this platform")
}
