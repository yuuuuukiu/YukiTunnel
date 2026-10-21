package cloudprofile

// IndexEntry is one row of the signed catalogue. It lives in this package so
// both the CI signer and the runtime registry share one definition.
type IndexEntry struct {
	ID        string   `json:"id" yaml:"id"`
	Vendor    string   `json:"vendor" yaml:"vendor"`
	Name      string   `json:"name" yaml:"name"`
	Version   string   `json:"version" yaml:"version"`
	Path      string   `json:"path" yaml:"path"`
	SHA256    string   `json:"sha256" yaml:"sha256"`
	Docs      []string `json:"docs,omitempty" yaml:"docs,omitempty"`
	MinEngine string   `json:"min_engine,omitempty" yaml:"min_engine,omitempty"`
}
