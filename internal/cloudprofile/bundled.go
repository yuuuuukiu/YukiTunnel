package cloudprofile

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"gopkg.in/yaml.v3"
)

// bundledProfiles ships the official profiles inside the binary so that a
// fresh install behind a firewall (or with every mirror blocked) still has a
// working, verifiable set. These documents are byte-identical to the ones in
// profiles/ and are covered by the same tests.
//
//go:embed all:bundled/*.yaml
var bundledFS embed.FS

// BundledProfiles returns the embedded profiles, parsed and validated.
//
// They are intentionally NOT trusted over the network: a caller should prefer a
// verified registry copy and fall back to these only when the registry is
// unreachable.
func BundledProfiles() ([]*Profile, error) {
	entries, err := fs.Glob(bundledFS, "bundled/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	out := make([]*Profile, 0, len(entries))
	for _, name := range entries {
		raw, err := fs.ReadFile(bundledFS, name)
		if err != nil {
			return nil, err
		}
		var p Profile
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("bundled %s: %w", path.Base(name), err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("bundled %s: %w", path.Base(name), err)
		}
		p.SHA256 = Digest(raw)
		p.Source = "builtin:" + path.Base(name)
		out = append(out, &p)
	}
	return out, nil
}

// BundledProfile looks up one embedded profile by id.
func BundledProfile(id string) (*Profile, error) {
	all, err := BundledProfiles()
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if p.Metadata.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("profile %q is not bundled in this build", id)
}

// BundledIndexEntry is a convenience view for the CLI/UI listing.
type BundledIndexEntry struct {
	ID      string
	Vendor  string
	Name    string
	Version string
	SHA256  string
}

// BundledIndex lists the embedded profiles as catalogue entries.
func BundledIndex() ([]BundledIndexEntry, error) {
	all, err := BundledProfiles()
	if err != nil {
		return nil, err
	}
	out := make([]BundledIndexEntry, 0, len(all))
	for _, p := range all {
		out = append(out, BundledIndexEntry{
			ID: p.Metadata.ID, Vendor: p.Metadata.Vendor, Name: p.Metadata.Name,
			Version: p.Metadata.Version, SHA256: p.SHA256,
		})
	}
	return out, nil
}
