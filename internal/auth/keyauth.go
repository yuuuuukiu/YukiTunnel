package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ChallengeTTL bounds how long a signed nonce stays valid.
const ChallengeTTL = 2 * time.Minute

// ErrKeyMismatch indicates a signature that does not verify.
var ErrKeyMismatch = errors.New("signature does not match the registered public key")

// NormalizePublicKey accepts an OpenSSH authorized_keys line (optionally with
// a trailing comment) and returns the canonical wire format plus SHA256
// fingerprint.
func NormalizePublicKey(line string) (canonical, fingerprint string, err error) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil {
		return "", "", fmt.Errorf("not a valid SSH public key: %w", err)
	}
	return string(ssh.MarshalAuthorizedKey(pk)), ssh.FingerprintSHA256(pk), nil
}

// ParseRSAPrivateKeyPEM decodes a PKCS#1 or PKCS#8 PEM RSA private key.
func ParseRSAPrivateKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found in private key file")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	rk, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not an RSA key")
	}
	return rk, nil
}

// PublicKeyFromPrivatePEM derives the authorized_keys line and fingerprint from
// an RSA private key PEM.
func PublicKeyFromPrivatePEM(pemBytes []byte) (canonical, fingerprint string, err error) {
	key, err := ParseRSAPrivateKeyPEM(pemBytes)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", err
	}
	return string(ssh.MarshalAuthorizedKey(sshPub)), ssh.FingerprintSHA256(sshPub), nil
}

// SignChallenge signs the server nonce with the RSA private key using
// RSASSA-PKCS1-v1_5 over SHA-256, returning base64.
func SignChallenge(key *rsa.PrivateKey, nonce string) (string, error) {
	sum := sha256.Sum256([]byte("yukitunnel-key-auth:" + nonce))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyChallenge verifies a base64 signature over nonce against an
// authorized_keys line.
func VerifyChallenge(publicKeyLine, nonce, sigB64 string) error {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKeyLine))
	if err != nil {
		return fmt.Errorf("stored public key is invalid: %w", err)
	}
	cpk, ok := pk.(ssh.CryptoPublicKey)
	if !ok {
		return errors.New("stored key cannot be used for signatures")
	}
	rsaPub, ok := cpk.CryptoPublicKey().(*rsa.PublicKey)
	if !ok {
		return errors.New("only RSA keys are supported for challenge authentication")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return errors.New("signature is not valid base64")
	}
	sum := sha256.Sum256([]byte("yukitunnel-key-auth:" + nonce))
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, sum[:], sig); err != nil {
		return ErrKeyMismatch
	}
	return nil
}

// GenerateServerIdentity returns a PEM encoded RSA key used to sign short-lived
// control-plane tokens between yuki-server and its nodes.
func GenerateServerIdentity() (pemBytes []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
}
