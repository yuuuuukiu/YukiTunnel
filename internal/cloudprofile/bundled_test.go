package cloudprofile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestBundledProfilesMatchRepository guards against the embedded copies drifting
// from profiles/*.yaml. If this fails, re-run:
//
//	cp profiles/*.yaml internal/cloudprofile/bundled/
func TestBundledProfilesMatchRepository(t *testing.T) {
	bundled, err := BundledProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(bundled) != 6 {
		t.Fatalf("expected 6 bundled profiles, got %d", len(bundled))
	}
	for _, p := range bundled {
		repoPath := filepath.Join("..", "..", "profiles", p.Metadata.ID+".yaml")
		raw, err := os.ReadFile(repoPath)
		if err != nil {
			t.Fatalf("%s: read repo profile: %v", p.Metadata.ID, err)
		}
		if got := Digest(raw); got != p.SHA256 {
			t.Fatalf("%s: bundled sha %s != repo sha %s（请重新复制 profiles/ 到 bundled/）",
				p.Metadata.ID, p.SHA256, got)
		}
	}
}

func TestBundledProfileLookup(t *testing.T) {
	p, err := BundledProfile("aliyun-ecs-security-group")
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Vendor != "aliyun" || p.Source == "" {
		t.Fatalf("unexpected profile: %+v", p.Metadata)
	}
	if _, err := BundledProfile("does-not-exist"); err == nil {
		t.Fatal("expected error for unknown bundled profile")
	}
}

func TestBundledIndex(t *testing.T) {
	idx, err := BundledIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 6 {
		t.Fatalf("index size = %d", len(idx))
	}
	vendors := make([]string, 0, len(idx))
	for _, e := range idx {
		vendors = append(vendors, e.Vendor)
	}
	want := []string{"aliyun", "aws", "azure", "gcp", "huawei", "tencent"}
	if !reflect.DeepEqual(vendors, want) {
		t.Fatalf("vendors = %v want %v", vendors, want)
	}
}
