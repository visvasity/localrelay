// Copyright (c) 2026 Visvasity LLC

package servers

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/visvasity/syncmap"
)

// certMinter signs a fresh TLS server certificate for each localhost hostname on
// demand, using an on-disk name-constrained CA. Minted certificates are cached
// per hostname and selected by the TLS SNI in getCertificate.
type certMinter struct {
	caCert *x509.Certificate
	caKey  crypto.Signer
	cache  syncmap.Map[string, *tls.Certificate]
}

// newCertMinter loads the CA (cert.pem, key.pem) from caDir. It requires the CA
// to be a certificate authority carrying a name constraint permitting the
// dNSName "localhost", so its minted leaves can never stray outside the local
// namespace (§13.5/§13.6).
func newCertMinter(caDir string) (*certMinter, error) {
	certPEM, err := os.ReadFile(filepath.Join(caDir, "cert.pem"))
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(caDir, "key.pem"))
	if err != nil {
		return nil, fmt.Errorf("read CA key: %w", err)
	}
	caCert, err := parseCACert(certPEM)
	if err != nil {
		return nil, err
	}
	caKey, err := parseCAKey(keyPEM)
	if err != nil {
		return nil, err
	}
	if !caCert.IsCA {
		return nil, fmt.Errorf("certificate is not a CA")
	}
	if !permitsLocalhost(caCert.PermittedDNSDomains) {
		return nil, fmt.Errorf("CA does not carry a name constraint permitting the dNSName \"localhost\"")
	}
	return &certMinter{caCert: caCert, caKey: caKey}, nil
}

// getCertificate is a tls.Config.GetCertificate callback: it returns a cached or
// freshly-minted server certificate for the client's SNI hostname. A client that
// sends no SNI is served the "localhost" certificate.
func (m *certMinter) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := strings.ToLower(hello.ServerName)
	if host == "" {
		host = "localhost"
	}
	if err := validLocalhostName(host); err != nil {
		return nil, err
	}
	if cert, ok := m.cache.Load(host); ok {
		return cert, nil
	}
	cert, err := m.mint(host)
	if err != nil {
		return nil, err
	}
	actual, _ := m.cache.LoadOrStore(host, cert)
	return actual, nil
}

// mint signs a new serverAuth leaf certificate for host under the CA.
func (m *certMinter) mint(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.caCert, &key.PublicKey, m.caKey)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}, nil
}

// validLocalhostName reports whether host is "localhost" or a name under the
// localhost TLD with valid DNS labels.
func validLocalhostName(host string) error {
	if host == "localhost" {
		return nil
	}
	if !strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("host %q is not under the localhost TLD", host)
	}
	labels := strings.Split(host, ".")
	for _, label := range labels[:len(labels)-1] {
		if err := ValidName(label); err != nil {
			return fmt.Errorf("invalid label in host %q: %w", host, err)
		}
	}
	return nil
}

// permitsLocalhost reports whether the permitted dNSName subtrees include
// "localhost" (in the no-leading-dot form).
func permitsLocalhost(domains []string) bool {
	for _, d := range domains {
		if strings.EqualFold(strings.TrimPrefix(d, "."), "localhost") {
			return true
		}
	}
	return false
}

func parseCACert(pemData []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("no CERTIFICATE PEM block in CA cert")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseCAKey(pemData []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in CA key")
	}
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("CA key does not implement crypto.Signer")
		}
		return signer, nil
	default:
		return nil, fmt.Errorf("unsupported CA key PEM type %q", block.Type)
	}
}
