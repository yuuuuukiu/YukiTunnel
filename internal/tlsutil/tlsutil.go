// Package tlsutil creates and loads TLS material for the API, the control
// listener and the local GUI servers.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
	"unicode"
)

// Files describes where certificate material lives.
type Files struct {
	CertFile string
	KeyFile  string
}

// EnsureSelfSigned generates a certificate for hosts when files are missing.
// The certificate is valid for 10 years and covers localhost, the host name
// and any extra hosts/IPs supplied (public host, node host, domain suffix).
func EnsureSelfSigned(dir string, hosts []string) (Files, error) {
	f := Files{
		CertFile: filepath.Join(dir, "tls", "server.crt"),
		KeyFile:  filepath.Join(dir, "tls", "server.key"),
	}
	if _, err := os.Stat(f.CertFile); err == nil {
		if _, err := os.Stat(f.KeyFile); err == nil {
			return f, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(f.CertFile), 0o700); err != nil {
		return f, err
	}
	certPEM, keyPEM, err := GenerateSelfSigned(hosts)
	if err != nil {
		return f, err
	}
	if err := os.WriteFile(f.CertFile, certPEM, 0o644); err != nil {
		return f, err
	}
	if err := os.WriteFile(f.KeyFile, keyPEM, 0o600); err != nil {
		return f, err
	}
	return f, nil
}

// GenerateSelfSigned returns a PEM certificate/key pair valid for hosts.
func GenerateSelfSigned(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	cn := "yukitunnel"
	if len(hosts) > 0 && hosts[0] != "" {
		cn = hosts[0]
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{Organization: []string{"YukiTunnel"}, CommonName: cn},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost", "yukitunnel.local"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	seen := map[string]bool{"localhost": true, "yukitunnel.local": true}
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// Load builds a tls.Config from files. When no files are given it generates an
// in-memory self-signed certificate (used by the local GUI servers).
func Load(certFile, keyFile string, extraHosts []string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		certPEM, keyPEM, err := GenerateSelfSigned(extraHosts)
		if err != nil {
			return nil, err
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, err
		}
		return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
	}
	pair, err := tls.X509KeyPair(mustRead(certFile), mustRead(keyFile))
	if err != nil {
		return nil, fmt.Errorf("load TLS keypair: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

func mustRead(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// Fingerprint returns the SHA-256 fingerprint of a PEM certificate, formatted
// like "AB:CD:...". Clients pin this value on first use.
func Fingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("no certificate PEM found")
	}
	sum := sha256.Sum256(block.Bytes)
	return formatFingerprint(sum[:]), nil
}

// FingerprintFile reads a certificate file and returns its fingerprint.
func FingerprintFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return Fingerprint(b)
}

func formatFingerprint(b []byte) string {
	out := ""
	for i, v := range b {
		if i > 0 {
			out += ":"
		}
		out += hex.EncodeToString([]byte{v})
	}
	return out
}

// ServerTLSConfigForClient builds a client tls.Config. When fingerprint is
// non-empty the peer certificate must match it (TOFU pinning). insecure skips
// verification entirely and is only meant for local debugging.
func ServerTLSConfigForClient(insecure bool, fingerprint string) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if insecure {
		cfg.InsecureSkipVerify = true
		return cfg
	}
	if fingerprint != "" {
		want := normalizeFP(fingerprint)
		cfg.InsecureSkipVerify = true // we verify manually below
		cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("server presented no certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			if normalizeFP(formatFingerprint(sum[:])) != want {
				return fmt.Errorf("certificate fingerprint mismatch (server may have been reinstalled)")
			}
			return nil
		}
	}
	return cfg
}

// normalizeFP reduces a fingerprint to lowercase hex with separators removed,
// so a value pasted from openssl (uppercase, colon separated) matches the one we
// compute internally. Without the case fold, pinning silently failed for every
// operator who copied the certificate fingerprint by hand.
func normalizeFP(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if c == ':' || c == ' ' || c == '-' {
			continue
		}
		out = append(out, unicode.ToLower(c))
	}
	return string(out)
}
