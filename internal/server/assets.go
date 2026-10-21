package server

import (
	"embed"
	"io/fs"
)

// webAssets embeds the production WebUI build (web/dist copied to webdist).
// When the directory only contains this placeholder the server still starts and
// serves a JSON hint instead of the UI.
//
//go:embed all:webdist
var webAssets embed.FS

// embeddedWebUI returns the embedded bundle, or nil when it was not built.
func embeddedWebUI() fs.FS {
	sub, err := fs.Sub(webAssets, "webdist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
