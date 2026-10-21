// Package ids produces opaque, URL-safe identifiers.
package ids

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
)

// Token returns a 32-byte random secret encoded with base64url (no padding).
// The value is safe to put in an Authorization header or a URL path segment.
func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hex returns n random bytes hex encoded.
func Hex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
