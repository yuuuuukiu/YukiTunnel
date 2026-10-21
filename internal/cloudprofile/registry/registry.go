package registry

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile"
	"gopkg.in/yaml.v3"
)

// DefaultMirrors is the built-in fallback chain.
//
// Order matters for latency, not for trust: every mirror is untrusted
// transport, and the ed25519 signature check is what makes the content
// trustworthy. GitHub-hosted mirrors come first because they are available as
// soon as the repository is public, while the branded mirror depends on DNS and
// object storage being configured.
var DefaultMirrors = []string{
	// Fast CDN over the GitHub repo; works with zero infrastructure.
	"https://cdn.jsdelivr.net/gh/yuuuuukiu/yukitunnel-profiles@main",
	// Raw GitHub: same bytes, no CDN cache in front.
	"https://raw.githubusercontent.com/yuuuuukiu/yukitunnel-profiles/main",
	// Branded mirror backed by Cloudflare R2. Requires a DNS record for
	// tunnel.yuuuuukiu.top; until then this entry simply fails and the chain
	// moves on.
	"https://tunnel.yuuuuukiu.top",
}

// Options configures a Registry.
type Options struct {
	// Mirrors is the ordered fallback chain (official → jsDelivr → raw → custom).
	Mirrors []string
	// Proxy is an HTTP or SOCKS5 proxy URL applied to every fetch.
	Proxy string
	// TrustedKeys maps key id -> ed25519 public key.
	TrustedKeys map[string]ed25519.PublicKey
	// CacheDir stores verified documents for offline use.
	CacheDir string
	// TTL is how long a cached index is considered fresh.
	TTL time.Duration
	// Timeout bounds each HTTP request.
	Timeout time.Duration
	// UserAgent is sent on every request.
	UserAgent string
	// OnFallback is called when a mirror fails, for UI/CLI visibility.
	OnFallback func(mirror string, err error)
}

// Registry fetches and verifies profiles.
type Registry struct {
	opts   Options
	client *http.Client
	index  *Index
	source string
}

// New builds a registry.
func New(opts Options) (*Registry, error) {
	if len(opts.Mirrors) == 0 {
		opts.Mirrors = DefaultMirrors
	}
	if opts.TTL <= 0 {
		opts.TTL = 12 * time.Hour
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "yuki-server/profile-registry"
	}
	if len(opts.TrustedKeys) == 0 {
		return nil, errors.New("no trusted signing keys configured")
	}
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	if opts.Proxy != "" {
		u, err := url.Parse(opts.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy: %w", err)
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &Registry{
		opts:   opts,
		client: &http.Client{Transport: tr, Timeout: opts.Timeout},
	}, nil
}

// Source reports which mirror served the current index.
func (r *Registry) Source() string { return r.source }

// Refresh fetches index.json + index.json.sig from the mirror chain, verifying
// the signature for every candidate.
func (r *Registry) Refresh(ctx context.Context) (*Index, error) {
	var errs []string
	integrityFailure := false
	for _, mirror := range r.opts.Mirrors {
		idx, err := r.fetchFrom(ctx, mirror)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", mirror, err))
			if errors.Is(err, ErrVerification) {
				integrityFailure = true
			}
			if r.opts.OnFallback != nil {
				r.opts.OnFallback(mirror, err)
			}
			continue
		}
		r.index = idx
		r.source = mirror
		return idx, nil
	}
	// A mirror answered but its content failed verification: never fall back to
	// anything else (cache included), because the only safe interpretation is
	// "someone is tampering with this registry".
	if integrityFailure {
		return nil, fmt.Errorf("%w:\n  %s", ErrVerification, strings.Join(errs, "\n  "))
	}
	// Last resort: a previously verified cache entry.
	if idx, err := r.loadCache(); err == nil {
		r.index = idx
		r.source = "cache"
		return idx, nil
	}
	return nil, fmt.Errorf("all profile mirrors failed:\n  %s", strings.Join(errs, "\n  "))
}

// indexPathCandidates lists where a mirror may keep its signed index.
//
// The standalone registry repo (yukitunnel-profiles) keeps it under profiles/,
// while a plain R2/S3 bucket keeps it at the root. Trying both keeps the client
// compatible with either layout without configuration.
var indexPathCandidates = []string{"profiles/index.json", "index.json"}

// fetchFrom downloads and verifies the index from one mirror.
//
// Failure classification matters: a transport error means "this mirror is
// unreachable" (offline fallback is legitimate), while a missing or invalid
// signature means "this source cannot prove what it published" and must abort
// the whole chain.
func (r *Registry) fetchFrom(ctx context.Context, mirror string) (*Index, error) {
	base := strings.TrimRight(mirror, "/")
	var transportErr error
	for _, candidate := range indexPathCandidates {
		indexURL := base + "/" + candidate
		rawIndex, err := r.get(ctx, indexURL, 1<<20)
		if err != nil {
			transportErr = err
			continue
		}
		rawSig, err := r.get(ctx, indexURL+".sig", 8<<10)
		if err != nil {
			return nil, fmt.Errorf("%w: %s served %s without a valid signature: %v",
				ErrVerification, mirror, candidate, err)
		}
		idx, err := VerifyIndex(rawIndex, rawSig, r.opts.TrustedKeys)
		if err != nil {
			return nil, err
		}
		r.saveCache(rawIndex, rawSig)
		return idx, nil
	}
	if transportErr == nil {
		transportErr = fmt.Errorf("no index.json found at %s", base)
	}
	return nil, transportErr
}

// get performs a bounded GET against a mirror.
func (r *Registry) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", r.opts.UserAgent)
	req.Header.Set("Accept", "application/json, application/yaml, text/plain")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// Index returns the current index, refreshing if needed.
func (r *Registry) Index(ctx context.Context) (*Index, error) {
	if r.index != nil {
		return r.index, nil
	}
	return r.Refresh(ctx)
}

// List returns the profiles in the index, optionally filtered by vendor.
func (r *Registry) List(ctx context.Context, vendor string) ([]IndexProfile, error) {
	idx, err := r.Index(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]IndexProfile, 0, len(idx.Profiles))
	for _, p := range idx.Profiles {
		if vendor != "" && !strings.EqualFold(p.Vendor, vendor) {
			out = append(out, p) // keep for caller-side filtering visibility
			continue
		}
		out = append(out, p)
	}
	if vendor != "" {
		filtered := out[:0]
		for _, p := range out {
			if strings.EqualFold(p.Vendor, vendor) {
				filtered = append(filtered, p)
			}
		}
		out = filtered
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Load fetches and verifies one profile by id.
func (r *Registry) Load(ctx context.Context, id string) (*cloudprofile.Profile, error) {
	idx, err := r.Index(ctx)
	if err != nil {
		return nil, err
	}
	var entry *IndexProfile
	for i := range idx.Profiles {
		if idx.Profiles[i].ID == id {
			entry = &idx.Profiles[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("profile %q not found in index", id)
	}
	// Try each mirror until one yields bytes matching the signed hash.
	var errs []string
	for _, mirror := range r.opts.Mirrors {
		base := strings.TrimRight(mirror, "/")
		raw, err := r.get(ctx, base+"/"+strings.TrimLeft(entry.Path, "/"), 1<<20)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", mirror, err))
			continue
		}
		sum := sha256.Sum256(raw)
		got := hex.EncodeToString(sum[:])
		if !strings.EqualFold(got, entry.SHA256) {
			return nil, fmt.Errorf("%w: %s served a profile whose sha256 does not match the signed index (want %s, got %s)",
				ErrVerification, mirror, entry.SHA256, got)
		}
		var p cloudprofile.Profile
		if err := yaml.Unmarshal(raw, &p); err != nil {
			errs = append(errs, fmt.Sprintf("%s: parse: %v", mirror, err))
			continue
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %s served an invalid profile: %v", ErrVerification, mirror, err)
		}
		p.Source = mirror
		p.SHA256 = got
		return &p, nil
	}
	return nil, fmt.Errorf("could not load verified profile %q:\n  %s", id, strings.Join(errs, "\n  "))
}

// cache paths

func (r *Registry) cachePaths() (string, string) {
	if r.opts.CacheDir == "" {
		return "", ""
	}
	return filepath.Join(r.opts.CacheDir, "index.json"), filepath.Join(r.opts.CacheDir, "index.json.sig")
}

func (r *Registry) saveCache(rawIndex, rawSig []byte) {
	ip, sp := r.cachePaths()
	if ip == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(ip), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(ip, rawIndex, 0o600)
	_ = os.WriteFile(sp, rawSig, 0o600)
}

func (r *Registry) loadCache() (*Index, error) {
	ip, sp := r.cachePaths()
	if ip == "" {
		return nil, errors.New("no cache dir")
	}
	rawIndex, err := os.ReadFile(ip)
	if err != nil {
		return nil, err
	}
	rawSig, err := os.ReadFile(sp)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(ip)
	if err == nil && time.Since(info.ModTime()) > 30*24*time.Hour {
		return nil, errors.New("cache too old")
	}
	// Cached content is still signature-verified: never trust the disk alone.
	idx, err := VerifyIndex(rawIndex, rawSig, r.opts.TrustedKeys)
	if err != nil {
		return nil, fmt.Errorf("cached index failed verification: %w", err)
	}
	return idx, nil
}

// VerifyFile verifies a local profile file against an expected sha256 (used by
// `yuki-server profile verify`).
func VerifyFile(path, wantSHA string, trusted map[string]ed25519.PublicKey) (*cloudprofile.Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if wantSHA != "" && !strings.EqualFold(got, wantSHA) {
		return nil, fmt.Errorf("sha256 mismatch: want %s got %s", wantSHA, got)
	}
	var p cloudprofile.Profile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	p.SHA256 = got
	p.Source = "file:" + path
	return &p, nil
}
