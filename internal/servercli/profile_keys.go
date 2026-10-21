package servercli

import (
	"crypto/ed25519"

	"github.com/yuuuuukiu/yukitunnel/internal/server"
)

// trustedKeys delegates to the server's pinned key set.
//
// This used to be a second implementation that hardcoded the development seed,
// so `yuki-server profile index` rejected the index the very same binary
// accepted over HTTP. There must be exactly one source of truth for the keys
// that sign the registry.
func trustedKeys() map[string]ed25519.PublicKey {
	return server.TrustedProfileKeys()
}
