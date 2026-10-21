package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSelfSigned(t *testing.T) {
	certPEM, keyPEM, err := GenerateSelfSigned([]string{"127.0.0.1", "panel.example.com"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		t.Fatal("empty cert or key")
	}

	// The pair must actually load as a TLS certificate.
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("pair does not load: %v", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if len(cert.DNSNames) == 0 && len(cert.IPAddresses) == 0 {
		t.Fatal("cert has no SANs; browsers will reject it")
	}
	// The SANs we asked for must be present.
	names := strings.Join(cert.DNSNames, ",")
	if !strings.Contains(names, "panel.example.com") {
		t.Fatalf("requested DNS SAN missing: %v", cert.DNSNames)
	}
	if cert.NotAfter.Before(cert.NotBefore) {
		t.Fatal("invalid validity window")
	}
	// A self-signed cert is its own issuer.
	if cert.Issuer.String() != cert.Subject.String() {
		t.Fatalf("not self-signed: issuer=%v subject=%v", cert.Issuer, cert.Subject)
	}
}

func TestEnsureSelfSignedPersistsAndIsStable(t *testing.T) {
	dir := t.TempDir()
	f1, err := EnsureSelfSigned(dir, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := os.Stat(f1.CertFile); err != nil {
		t.Fatalf("cert file missing: %v", err)
	}
	if _, err := os.Stat(f1.KeyFile); err != nil {
		t.Fatalf("key file missing: %v", err)
	}

	// The key must not be world readable.
	info, _ := os.Stat(f1.KeyFile)
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("private key is too permissive: %o", mode)
	}

	// Calling again must reuse the existing material rather than rotate it.
	f2, err := EnsureSelfSigned(dir, []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if f2.CertFile != f1.CertFile {
		t.Fatal("cert path changed")
	}
	a, _ := os.ReadFile(f1.CertFile)
	b, _ := os.ReadFile(f2.CertFile)
	if string(a) != string(b) {
		t.Fatal("certificate was regenerated on the second call")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	files, err := EnsureSelfSigned(dir, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(files.CertFile, files.KeyFile, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion should be TLS 1.2, got %x", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(cfg.Certificates))
	}

	// Missing files must be an error, not a nil config.
	if _, err := Load(filepath.Join(dir, "nope.crt"), filepath.Join(dir, "nope.key"), nil); err == nil {
		t.Fatal("expected an error for missing files")
	}
}

func TestFingerprint(t *testing.T) {
	certPEM, _, err := GenerateSelfSigned([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	fp, err := Fingerprint(certPEM)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if fp == "" {
		t.Fatal("empty fingerprint")
	}
	// Colon-separated hex, stable across calls.
	if !strings.Contains(fp, ":") {
		t.Fatalf("fingerprint should be colon separated: %q", fp)
	}
	fp2, _ := Fingerprint(certPEM)
	if fp != fp2 {
		t.Fatal("fingerprint is not deterministic")
	}
	// A different certificate yields a different fingerprint.
	otherPEM, _, _ := GenerateSelfSigned([]string{"127.0.0.1"})
	otherFP, _ := Fingerprint(otherPEM)
	if otherFP == fp {
		t.Fatal("two certificates share a fingerprint")
	}
	if _, err := Fingerprint([]byte("not pem")); err == nil {
		t.Fatal("expected an error for garbage input")
	}
}

func TestFingerprintFile(t *testing.T) {
	dir := t.TempDir()
	files, _ := EnsureSelfSigned(dir, []string{"127.0.0.1"})
	fp, err := FingerprintFile(files.CertFile)
	if err != nil {
		t.Fatalf("fingerprint file: %v", err)
	}
	direct, _ := Fingerprint(mustRead(files.CertFile))
	if fp != direct {
		t.Fatal("FingerprintFile disagrees with Fingerprint")
	}
}

func TestNormalizeFP(t *testing.T) {
	// Formatting differences must not defeat fingerprint pinning.
	a := normalizeFP("AA:BB:CC")
	b := normalizeFP("aabbcc")
	if a != b {
		t.Fatalf("normalisation failed: %q vs %q", a, b)
	}
}

func TestServerTLSConfigForClient(t *testing.T) {
	// Insecure mode skips verification.
	cfg := ServerTLSConfigForClient(true, "")
	if cfg.InsecureSkipVerify != true {
		t.Fatal("insecure mode did not disable verification")
	}

	// With a pinned fingerprint, verification must be enforced by our callback.
	certPEM, _, _ := GenerateSelfSigned([]string{"127.0.0.1"})
	fp, _ := Fingerprint(certPEM)
	pinned := ServerTLSConfigForClient(false, fp)
	// Pinning disables Go's chain validation on purpose and substitutes our own
	// exact-match check; the callback is what actually protects the connection.
	if pinned.VerifyPeerCertificate == nil {
		t.Fatal("pinned mode needs a verification callback")
	}
	// The callback must reject a certificate whose fingerprint differs.
	otherPEM, _, _ := GenerateSelfSigned([]string{"127.0.0.1"})
	otherBlock, _ := pem.Decode(otherPEM)
	if err := pinned.VerifyPeerCertificate([][]byte{otherBlock.Bytes}, nil); err == nil {
		t.Fatal("pinned callback accepted a different certificate")
	}
	// And accept the matching one.
	block, _ := pem.Decode(certPEM)
	if err := pinned.VerifyPeerCertificate([][]byte{block.Bytes}, nil); err != nil {
		t.Fatalf("pinned callback rejected the correct certificate: %v", err)
	}
	// Case and separator differences must not break a pinned fingerprint.
	if got := normalizeFP(strings.ToUpper(fp)); got != normalizeFP(fp) {
		t.Fatalf("uppercase fingerprint not normalised: %q vs %q", got, normalizeFP(fp))
	}
}
