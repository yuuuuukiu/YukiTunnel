// Package cloudprofile implements declarative cloud firewall profiles: a
// signed YAML document describes how to open/close a port on a cloud provider,
// and a small engine renders and signs the provider request.
//
// The design goal is that adding a provider never requires new Go code: only a
// new signed profile that declares its port format, protocol case, idempotency
// strategy and signing engine.
package cloudprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// APIVersion is the schema version accepted by this build.
const APIVersion = "yukitunnel.io/v1"

// Profile is one signed provider declaration.
type Profile struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`

	// Integrity is filled by the registry after signature verification. It is
	// never read from the document itself.
	Source string `yaml:"-" json:"source,omitempty"`
	SHA256 string `yaml:"-" json:"sha256,omitempty"`
}

// Metadata identifies a profile.
type Metadata struct {
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Vendor      string   `yaml:"vendor" json:"vendor"`
	Version     string   `yaml:"version" json:"version"`
	Maintainer  string   `yaml:"maintainer" json:"maintainer"`
	Description string   `yaml:"description" json:"description"`
	Docs        []string `yaml:"docs" json:"docs"`
	// MinEngine guards against a profile that needs a newer engine.
	MinEngine string `yaml:"min_engine" json:"min_engine"`
}

// Spec is the executable part of a profile.
type Spec struct {
	Auth         Auth              `yaml:"auth" json:"auth"`
	Endpoint     Endpoint          `yaml:"endpoint" json:"endpoint"`
	AllowedHosts []string          `yaml:"allowed_hosts" json:"allowed_hosts"`
	Permissions  []string          `yaml:"permissions" json:"permissions"`
	Parameters   []Parameter       `yaml:"parameters" json:"parameters"`
	Actions      map[string]Action `yaml:"actions" json:"actions"`
	// RevokePolicy tells the engine how to remove a rule.
	RevokePolicy RevokePolicy `yaml:"revoke_policy" json:"revoke_policy"`
	// RateLimit protects the provider API (requests per minute).
	RateLimit int `yaml:"rate_limit_per_minute" json:"rate_limit_per_minute"`
}

// Auth selects the built-in signing engine.
type Auth struct {
	Engine           string   `yaml:"engine" json:"engine"`
	CredentialFields []string `yaml:"credential_fields" json:"credential_fields"`
	// Service/Version are used by engines that need them (AWS ec2, Tencent vpc).
	Service string `yaml:"service" json:"service"`
	Version string `yaml:"version" json:"version"`
}

// Endpoint describes how to build the request URL.
type Endpoint struct {
	Template    string            `yaml:"template" json:"template"`
	Method      string            `yaml:"method" json:"method"`
	ContentType string            `yaml:"content_type" json:"content_type"`
	Headers     map[string]string `yaml:"headers" json:"headers"`
	Query       map[string]string `yaml:"query" json:"query"`
}

// Parameter is a value the operator must provide.
type Parameter struct {
	Name        string `yaml:"name" json:"name"`
	Label       string `yaml:"label" json:"label"`
	Required    bool   `yaml:"required" json:"required"`
	Default     string `yaml:"default" json:"default"`
	Example     string `yaml:"example" json:"example"`
	Secret      bool   `yaml:"secret" json:"secret"`
	Description string `yaml:"description" json:"description"`
	// Enum restricts allowed values (e.g. protocol list).
	Enum []string `yaml:"enum" json:"enum"`
}

// Action is one operation (ensure_port_allowed / revoke_port_allowed).
type Action struct {
	Operation string `yaml:"operation" json:"operation"`
	// Method overrides endpoint.method for this action only. A profile whose
	// create is PUT but whose revoke is DELETE cannot express that with a
	// single endpoint method, and silently sending PUT with an empty body
	// clears the rule instead of removing it.
	Method          string           `yaml:"method" json:"method,omitempty"`
	Params          map[string]any   `yaml:"params" json:"params"`
	IdempotentCheck *IdempotentCheck `yaml:"idempotent_check" json:"idempotent_check"`
	SuccessWhen     *SuccessWhen     `yaml:"success_when" json:"success_when"`
	// Body wraps params for JSON APIs that nest the payload.
	Body *BodySpec `yaml:"body" json:"body"`
}

// BodySpec wraps the rendered params into a JSON envelope, e.g.
// {"security_group_rule": {...}} for Huawei or {"IpPermissions.1": ...}.
type BodySpec struct {
	Envelope   string `yaml:"envelope" json:"envelope"`
	ArrayField string `yaml:"array_field" json:"array_field"`
}

// IdempotentCheck runs a read-only call first so repeat runs are free.
type IdempotentCheck struct {
	Operation string         `yaml:"operation" json:"operation"`
	Params    map[string]any `yaml:"params" json:"params"`
	Match     MatchSpec      `yaml:"match" json:"match"`
}

// MatchSpec declares how to find an existing rule in the response.
type MatchSpec struct {
	// Path is a dot path to the rule array, e.g. "Permissions.Permission".
	Path string `yaml:"path" json:"path"`
	// Fields maps response field -> template producing the expected value.
	Fields map[string]string `yaml:"fields" json:"fields"`
	// Compare is "port_range" | "exact" | "subset".
	Compare string `yaml:"compare" json:"compare"`
}

// SuccessWhen declares the success predicate.
type SuccessWhen struct {
	Status     int      `yaml:"status" json:"status"`
	ErrorCodes []string `yaml:"error_codes" json:"error_codes"`
	// DuplicateCodes are treated as success (rule already exists).
	DuplicateCodes []string `yaml:"duplicate_codes" json:"duplicate_codes"`
}

// RevokePolicy tells the engine how to address an existing rule.
type RevokePolicy struct {
	By string `yaml:"by" json:"by"` // same_params | rule_id | rule_name | policy_index
	// NameTemplate is used when By == rule_name.
	NameTemplate string `yaml:"name_template" json:"name_template"`
}

// ID returns the profile identifier.
func (p *Profile) ID() string { return p.Metadata.ID }

// Digest returns the sha256 of the raw document bytes.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Validate performs schema-level checks before a profile can be executed.
func (p *Profile) Validate() error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }

	if p.APIVersion != APIVersion {
		add("apiVersion %q is not supported (want %q)", p.APIVersion, APIVersion)
	}
	if p.Kind != "CloudProfile" {
		add("kind must be CloudProfile, got %q", p.Kind)
	}
	if !idPattern.MatchString(p.Metadata.ID) {
		add("metadata.id %q must match %s", p.Metadata.ID, idPattern.String())
	}
	if p.Metadata.Vendor == "" {
		add("metadata.vendor is required")
	}
	if p.Spec.Auth.Engine == "" {
		add("spec.auth.engine is required")
	}
	if len(p.Spec.Auth.CredentialFields) == 0 {
		add("spec.auth.credential_fields is required")
	}
	if p.Spec.Endpoint.Template == "" {
		add("spec.endpoint.template is required")
	}
	if len(p.Spec.AllowedHosts) == 0 {
		add("spec.allowed_hosts is required (prevents credential exfiltration)")
	}
	if len(p.Spec.Permissions) == 0 {
		add("spec.permissions is required (shown to the operator before execution)")
	}
	for _, name := range []string{ActionEnsurePortAllowed, ActionRevokePortAllowed} {
		a, ok := p.Spec.Actions[name]
		if !ok {
			add("spec.actions.%s is required", name)
			continue
		}
		if a.Operation == "" {
			add("spec.actions.%s.operation is required", name)
		}
	}
	if p.Spec.Actions[ActionEnsurePortAllowed].IdempotentCheck == nil {
		add("spec.actions.ensure_port_allowed.idempotent_check is required")
	}
	switch p.Spec.RevokePolicy.By {
	case "same_params", "rule_id", "rule_name", "policy_index":
	case "":
		add("spec.revoke_policy.by is required")
	default:
		add("spec.revoke_policy.by %q is not supported", p.Spec.RevokePolicy.By)
	}
	if len(problems) > 0 {
		return fmt.Errorf("profile %s: %s", p.Metadata.ID, strings.Join(problems, "; "))
	}
	return nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,62}$`)

// Action names shared by every profile.
const (
	ActionEnsurePortAllowed = "ensure_port_allowed"
	ActionRevokePortAllowed = "revoke_port_allowed"
)

// Request describes one rendered provider call.
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"-"`
	// FormBody is set for RPC-style providers (Aliyun, AWS query API).
	FormBody  map[string]string `json:"-"`
	Operation string            `json:"operation"`
}

// ExecutionResult is what the engine returns to the caller.
type ExecutionResult struct {
	ProfileID   string        `json:"profile_id"`
	ProfileVer  string        `json:"profile_version"`
	ProfileHash string        `json:"profile_sha256"`
	Action      string        `json:"action"`
	Operation   string        `json:"operation"`
	Host        string        `json:"host"`
	Status      int           `json:"status"`
	AlreadyDone bool          `json:"already_done"`
	Skipped     bool          `json:"skipped"`
	Message     string        `json:"message"`
	Duration    time.Duration `json:"duration_ms"`
	RequestID   string        `json:"request_id,omitempty"`
	// RequestPreview is only populated for dry-runs with ShowRequest, so an
	// operator can verify the exact call before it reaches the cloud.
	RequestPreview *RequestPreview `json:"request_preview,omitempty"`
}

// RequestPreview is a redacted, human-readable view of a rendered request.
//
// Credentials are never included: the Authorization header is replaced with a
// placeholder before the request is described, so a preview can be pasted into
// an issue without leaking a token.
type RequestPreview struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}
