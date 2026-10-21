package servercli

import (
	"github.com/yuuuuukiu/yukitunnel/internal/store"
)

// Credential encryption is shared with the server (internal/store), so a
// credential written by the CLI is readable by the API and vice versa.
func (l *LocalBackend) seal(fields map[string]string) (string, error) {
	return store.SealCredentials(l.cfg.DataDir, fields)
}

func (l *LocalBackend) open(sealed string) (map[string]string, error) {
	return store.OpenCredentials(l.cfg.DataDir, sealed)
}
