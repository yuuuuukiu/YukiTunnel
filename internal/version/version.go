// Package version holds build metadata for every YukiTunnel binary.
package version

// Version is the semantic version of the current build. It is overwritten at
// release time via -ldflags "-X .../internal/version.Version=..."
var Version = "0.1.0"

// Commit is the git commit the binary was built from.
var Commit = "dev"
