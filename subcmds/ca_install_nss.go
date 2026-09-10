// Copyright (c) 2026 Visvasity LLC

package subcmds

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Firefox and Chrome/Chromium do not use the system trust store; they keep their
// own NSS certificate databases, managed with the `certutil` tool. Chrome/Chromium
// uses a shared per-user database (e.g. ~/.pki/nssdb); Firefox keeps one per
// profile. The discovery below follows mkcert across native, Snap, Flatpak, and
// macOS layouts.

// caNickname returns a stable NSS nickname for the CA: its certificate subject
// Common Name, or a fixed default if that can't be read.
func caNickname(certPath string) string {
	if data, err := os.ReadFile(certPath); err == nil {
		if block, _ := pem.Decode(data); block != nil {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil && cert.Subject.CommonName != "" {
				return cert.Subject.CommonName
			}
		}
	}
	return "localrelay Local CA"
}

// permitsLocalhost reports whether the permitted dNSName subtrees include
// "localhost" in the no-leading-dot form.
func permitsLocalhost(domains []string) bool {
	for _, d := range domains {
		if strings.EqualFold(strings.TrimPrefix(d, "."), "localhost") {
			return true
		}
	}
	return false
}

// localhostCACert parses the first CERTIFICATE PEM block in pemData and returns it
// only if it is a CA name-constrained to the dNSName "localhost". Uninstall uses
// this to guarantee it never removes an unrelated certificate.
func localhostCACert(pemData []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no certificate PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, errors.New("certificate is not a CA")
	}
	if !permitsLocalhost(cert.PermittedDNSDomains) {
		return nil, errors.New(`certificate is not name-constrained to "localhost"`)
	}
	return cert, nil
}

func binaryExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// certutilInstallHint returns the package-manager command that installs certutil
// for the current system, mirroring mkcert. On macOS certutil ships with the
// Homebrew nss formula, so suggest it regardless of whether brew is on PATH yet.
func certutilInstallHint() string {
	if runtime.GOOS == "darwin" {
		return "brew install nss"
	}
	switch {
	case binaryExists("apt"):
		return "apt install libnss3-tools"
	case binaryExists("apt-get"):
		return "apt-get install libnss3-tools"
	case binaryExists("dnf"):
		return "dnf install nss-tools"
	case binaryExists("yum"):
		return "yum install nss-tools"
	case binaryExists("zypper"):
		return "zypper install mozilla-nss-tools"
	case binaryExists("pacman"):
		return "pacman -S nss"
	case binaryExists("brew"):
		return "brew install nss"
	default:
		return "your system's NSS tools package"
	}
}

// certutilPath locates the certutil tool, checking the Homebrew nss location on
// macOS, and returns a package-specific install hint if absent.
func certutilPath() (string, error) {
	if p, err := exec.LookPath("certutil"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{"/opt/homebrew/opt/nss/bin/certutil", "/usr/local/opt/nss/bin/certutil"} {
			if fileExists(p) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("certutil not found; install it with %q", certutilInstallHint())
}

// addToNSSDB installs certPath as a TLS-trusted CA ("C,," trust flags) into the
// NSS database dbSpec (e.g. "sql:/path" or "dbm:/path"), replacing any existing
// entry with the same nickname so re-installs don't duplicate.
func addToNSSDB(ctx context.Context, certutil, dbSpec, certPath, nickname string) error {
	_ = exec.CommandContext(ctx, certutil, "-D", "-d", dbSpec, "-n", nickname).Run()
	out, err := exec.CommandContext(ctx, certutil, "-A", "-d", dbSpec, "-t", "C,,", "-n", nickname, "-i", certPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil -A on %s: %w: %s", dbSpec, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// chromeNSSDirs lists the NSS databases Chrome/Chromium reads for the current user
// across native and Snap layouts.
func chromeNSSDirs() []string {
	home := os.Getenv("HOME")
	dirs := []string{filepath.Join(home, ".pki", "nssdb")}
	if runtime.GOOS == "linux" {
		dirs = append(dirs, filepath.Join(home, "snap", "chromium", "current", ".pki", "nssdb"))
	}
	return dirs
}

// presentChromeNSSDirs returns the Chrome NSS databases that are initialized.
func presentChromeNSSDirs() []string {
	var out []string
	for _, d := range chromeNSSDirs() {
		if fileExists(filepath.Join(d, "cert9.db")) {
			out = append(out, d)
		}
	}
	return out
}

// chromePresent reports whether the current user has an initialized Chrome NSS
// database, so the default install can skip Chrome when it is absent.
func chromePresent() bool {
	return len(presentChromeNSSDirs()) > 0
}

// firefoxPresent reports whether the current user has at least one initialized
// Firefox profile.
func firefoxPresent() bool {
	for _, p := range firefoxProfiles() {
		if fileExists(filepath.Join(p, "cert9.db")) || fileExists(filepath.Join(p, "cert8.db")) {
			return true
		}
	}
	return false
}

// installChromeCA installs the CA into every initialized Chrome/Chromium NSS
// database for the current user, creating and initializing the default one if
// none exists. Returns the number of databases updated.
func installChromeCA(ctx context.Context, certPath, nickname string) (int, error) {
	certutil, err := certutilPath()
	if err != nil {
		return 0, err
	}
	dirs := presentChromeNSSDirs()
	if len(dirs) == 0 {
		def := filepath.Join(os.Getenv("HOME"), ".pki", "nssdb")
		if err := os.MkdirAll(def, 0o700); err != nil {
			return 0, err
		}
		if out, err := exec.CommandContext(ctx, certutil, "-N", "-d", "sql:"+def, "--empty-password").CombinedOutput(); err != nil {
			return 0, fmt.Errorf("initialize NSS db %s: %w: %s", def, err, strings.TrimSpace(string(out)))
		}
		dirs = []string{def}
	}
	n := 0
	for _, d := range dirs {
		if err := addToNSSDB(ctx, certutil, "sql:"+d, certPath, nickname); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// installFirefoxCA installs the CA into every initialized Firefox profile for the
// current user, returning the number of profiles updated.
func installFirefoxCA(ctx context.Context, certPath, nickname string) (int, error) {
	certutil, err := certutilPath()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range firefoxProfiles() {
		dbSpec := nssProfileDB(p)
		if dbSpec == "" {
			continue
		}
		if err := addToNSSDB(ctx, certutil, dbSpec, certPath, nickname); err != nil {
			return n, fmt.Errorf("Firefox profile %s: %w", p, err)
		}
		n++
	}
	if n == 0 {
		return 0, errors.New("no initialized Firefox profiles found")
	}
	return n, nil
}

// nssProfileDB returns the certutil "-d" spec for an NSS profile directory:
// "sql:" for the modern cert9.db backend, "dbm:" for the legacy cert8.db backend,
// or "" if the directory is not an initialized profile.
func nssProfileDB(dir string) string {
	switch {
	case fileExists(filepath.Join(dir, "cert9.db")):
		return "sql:" + dir
	case fileExists(filepath.Join(dir, "cert8.db")):
		return "dbm:" + dir
	default:
		return ""
	}
}

// firefoxProfiles returns candidate Firefox profile directories for the current
// user across native, Snap, Flatpak (Linux), and macOS installs.
func firefoxProfiles() []string {
	home := os.Getenv("HOME")
	var roots []string
	switch runtime.GOOS {
	case "darwin":
		roots = []string{filepath.Join(home, "Library", "Application Support", "Firefox", "Profiles")}
	default: // linux and other unix
		roots = []string{
			filepath.Join(home, ".mozilla", "firefox"),
			filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox"),
			filepath.Join(home, ".var", "app", "org.mozilla.firefox", ".mozilla", "firefox"),
		}
	}
	var profiles []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				profiles = append(profiles, filepath.Join(root, e.Name()))
			}
		}
	}
	return profiles
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
