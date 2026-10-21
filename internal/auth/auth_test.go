package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestPasswordHashing(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected an argon2id PHC string, got %q", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("hash must not contain the plaintext")
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
	// The same password must hash differently (unique salt).
	hash2, _ := HashPassword("correct horse battery staple")
	if hash == hash2 {
		t.Fatal("two hashes of the same password are identical; salt is not random")
	}
	// Malformed hashes must be rejected, not panic.
	for _, bad := range []string{"", "not-a-hash", "$argon2id$v=19$m=1,t=1,p=1$xx$yy"} {
		if VerifyPassword(bad, "anything") {
			t.Fatalf("malformed hash %q was accepted", bad)
		}
	}
}

func TestTokens(t *testing.T) {
	plain, hash := NewToken()
	if len(plain) < 32 {
		t.Fatalf("token too short: %d", len(plain))
	}
	if plain == hash {
		t.Fatal("token must not be stored in plaintext")
	}
	if HashToken(plain) != hash {
		t.Fatal("HashToken is not deterministic")
	}
	if HashToken(plain+"x") == hash {
		t.Fatal("different tokens produced the same hash")
	}
	if !strings.HasPrefix(plain, "yk_") {
		t.Fatalf("expected a yk_ prefix, got %q", plain[:8])
	}
	if TokenPrefix(plain) == "" || len(TokenPrefix(plain)) >= len(plain) {
		t.Fatalf("prefix should be a short non-secret fragment: %q", TokenPrefix(plain))
	}
	// Two tokens must differ.
	p2, _ := NewToken()
	if p2 == plain {
		t.Fatal("token collision")
	}
}

func TestParseTTLHours(t *testing.T) {
	cases := map[string]int{
		"":    0,
		"24":  24,
		"168": 168,
		"0":   0,
		"abc": 0,
		"-5":  0,
	}
	for in, want := range cases {
		if got, _ := ParseTTLHours(in); got != want {
			t.Errorf("ParseTTLHours(%q) = %d, want %d", in, got, want)
		}
	}
}

func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pemPKCS1(t *testing.T, k *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func TestKeyAuthRoundTrip(t *testing.T) {
	key := newTestKey(t)
	privPEM := pemPKCS1(t, key)

	canonical, fingerprint, err := PublicKeyFromPrivatePEM(privPEM)
	if err != nil {
		t.Fatalf("derive public key: %v", err)
	}
	if !strings.HasPrefix(canonical, "ssh-rsa ") {
		t.Fatalf("expected an authorized_keys line, got %q", canonical[:16])
	}
	if fingerprint == "" {
		t.Fatal("empty fingerprint")
	}

	// The canonical form must normalise back to the same value.
	again, fp2, err := NormalizePublicKey(canonical)
	if err != nil || again != canonical || fp2 != fingerprint {
		t.Fatalf("normalisation is not idempotent: %v", err)
	}

	// A signed challenge verifies.
	nonce := "abc123"
	sig, err := SignChallenge(key, nonce)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := VerifyChallenge(canonical, nonce, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Wrong nonce, wrong key and garbage signatures must all fail.
	if err := VerifyChallenge(canonical, "other", sig); err == nil {
		t.Fatal("signature accepted for the wrong nonce")
	}
	otherCanonical, _, _ := PublicKeyFromPrivatePEM(pemPKCS1(t, newTestKey(t)))
	if err := VerifyChallenge(otherCanonical, nonce, sig); err == nil {
		t.Fatal("signature accepted for the wrong key")
	}
	if err := VerifyChallenge(canonical, nonce, "not-base64!!"); err == nil {
		t.Fatal("garbage signature accepted")
	}
}

func TestNormalizePublicKeyRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not-a-key", "ssh-rsa", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI"} {
		if _, _, err := NormalizePublicKey(bad); err == nil {
			t.Fatalf("accepted invalid key %q", bad)
		}
	}
}

func TestParsePrivateKeyRejectsGarbage(t *testing.T) {
	if _, err := ParseRSAPrivateKeyPEM([]byte("garbage")); err == nil {
		t.Fatal("accepted garbage PEM")
	}
	if _, err := ParseRSAPrivateKeyPEM(nil); err == nil {
		t.Fatal("accepted empty PEM")
	}
}

func TestGenerateServerIdentity(t *testing.T) {
	pemBytes, err := GenerateServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRSAPrivateKeyPEM(pemBytes); err != nil {
		t.Fatalf("generated identity is not parseable: %v", err)
	}
	// Each call must produce a distinct key.
	other, _ := GenerateServerIdentity()
	if string(other) == string(pemBytes) {
		t.Fatal("two identities are identical")
	}
}

// TestPublicKeyFromPrivatePEMAnyAcceptsOpenSSH covers the format users actually
// paste: an OpenSSH private key ("BEGIN OPENSSH PRIVATE KEY") rather than a
// PKCS#1 PEM. Rejecting it would break the documented CLI flow.
func TestPublicKeyFromPrivatePEMAnyAcceptsOpenSSH(t *testing.T) {
	key := newTestKey(t)

	// Build an OpenSSH private key blob.
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	opensshPEM := pem.EncodeToMemory(block)
	if !strings.Contains(string(opensshPEM), "OPENSSH PRIVATE KEY") {
		t.Fatalf("expected an OpenSSH key, got %q", string(opensshPEM)[:40])
	}

	canonical, fingerprint, err := PublicKeyFromPrivatePEMAny(opensshPEM)
	if err != nil {
		t.Fatalf("OpenSSH key rejected: %v", err)
	}
	if !strings.HasPrefix(canonical, "ssh-rsa ") || fingerprint == "" {
		t.Fatalf("unexpected result: %q %q", canonical, fingerprint)
	}

	// The PKCS#1 path must yield the same identity.
	pkcs1Canonical, pkcs1FP, err := PublicKeyFromPrivatePEMAny(pemPKCS1(t, key))
	if err != nil {
		t.Fatalf("PKCS#1 key rejected: %v", err)
	}
	if pkcs1Canonical != canonical || pkcs1FP != fingerprint {
		t.Fatal("the two encodings of the same key disagree")
	}

	// And the private key parsed from OpenSSH form can sign.
	parsed, err := ParsePrivateKeyPEM(opensshPEM)
	if err != nil {
		t.Fatalf("parse openssh: %v", err)
	}
	sig, _ := SignChallenge(parsed, "nonce")
	if err := VerifyChallenge(canonical, "nonce", sig); err != nil {
		t.Fatalf("signature from openssh key did not verify: %v", err)
	}
}
