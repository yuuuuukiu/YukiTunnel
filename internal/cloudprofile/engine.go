package cloudprofile

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

// Signer is implemented by every built-in signing engine.
type Signer interface {
	// Name is the identifier referenced by spec.auth.engine.
	Name() string
	// Sign mutates the request (headers/query/body) so the provider accepts it.
	Sign(ctx context.Context, req *Request, creds Credentials) error
}

// Credentials holds the operator's cloud credentials. Values are never logged.
type Credentials map[string]string

// Engine executes profiles.
type Engine struct {
	signers map[string]Signer
	client  *http.Client
	// Now is injectable for tests.
	Now func() time.Time
	// MaxResponseBytes caps provider responses (1 MiB by default).
	MaxResponseBytes int64
}

// NewEngine builds an engine with the built-in signers and the given proxy.
func NewEngine(proxyURL string) (*Engine, error) {
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
		DialContext:         (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy %q: %w", proxyURL, err)
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
		}
		tr.Proxy = http.ProxyURL(u)
	}
	e := &Engine{
		signers:          map[string]Signer{},
		client:           &http.Client{Transport: tr, Timeout: 30 * time.Second},
		Now:              time.Now,
		MaxResponseBytes: 1 << 20,
	}
	e.Register(NewAliyunSigner())
	e.Register(NewTencentSigner())
	e.Register(NewAWSSigV4Signer())
	e.Register(NewHuaweiSigner())
	e.Register(NewAzureSigner())
	e.Register(NewGCPSigner())
	return e, nil
}

// HTTPClientForTest replaces the HTTP client (test-only helper).
func (e *Engine) HTTPClientForTest(c *http.Client) {
	if c != nil {
		e.client = c
	}
}

// Register adds or replaces a signer.
func (e *Engine) Register(s Signer) { e.signers[s.Name()] = s }

// Signers lists the available engines (used by `yuki-server profile engines`).
func (e *Engine) Signers() []string {
	out := make([]string, 0, len(e.signers))
	for name := range e.signers {
		out = append(out, name)
	}
	return out
}

// RunOptions carries per-execution inputs.
type RunOptions struct {
	Action string
	Params map[string]any
	Creds  Credentials
	DryRun bool
	// ShowRequest includes the rendered request in a dry-run result, so an
	// operator can confirm the exact method, URL, headers and body before any
	// cloud call is made. Off by default because the body can be large.
	ShowRequest bool
	// ExtraHeaders lets the caller add tracing headers (never credentials).
	ExtraHeaders map[string]string
}

// Run renders and executes one action from a profile.
func (e *Engine) Run(ctx context.Context, p *Profile, opts RunOptions) (*ExecutionResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	action, ok := p.Spec.Actions[opts.Action]
	if !ok {
		return nil, fmt.Errorf("profile %s has no action %q", p.Metadata.ID, opts.Action)
	}
	signer, ok := e.signers[p.Spec.Auth.Engine]
	if !ok {
		return nil, fmt.Errorf("signing engine %q is not available", p.Spec.Auth.Engine)
	}
	data, err := e.buildData(p, opts.Params)
	if err != nil {
		return nil, err
	}

	// 1) Idempotency probe: if the rule already exists we stop here.
	if action.IdempotentCheck != nil {
		done, detail, err := e.probe(ctx, p, action.IdempotentCheck, data, opts)
		if err != nil {
			return nil, fmt.Errorf("idempotent check failed: %w", err)
		}
		if done {
			return &ExecutionResult{
				ProfileID: p.Metadata.ID, ProfileVer: p.Metadata.Version, ProfileHash: p.SHA256,
				Action: opts.Action, Operation: action.IdempotentCheck.Operation,
				AlreadyDone: true, Skipped: true, Message: "规则已存在，无需变更（" + detail + "）",
			}, nil
		}
	}

	// 2) Render the mutating request.
	req, err := e.buildRequest(p, action, data, opts.Action)
	if err != nil {
		return nil, err
	}
	if err := e.checkHost(p, req.URL); err != nil {
		return nil, err
	}
	if err := signer.Sign(ctx, req, opts.Creds); err != nil {
		return nil, fmt.Errorf("sign with %s: %w", signer.Name(), err)
	}
	if opts.DryRun {
		res := &ExecutionResult{
			ProfileID: p.Metadata.ID, ProfileVer: p.Metadata.Version, ProfileHash: p.SHA256,
			Action: opts.Action, Operation: action.Operation,
			Host: mustHost(req.URL), Skipped: true,
			Message: fmt.Sprintf("dry-run: %s %s（未发送请求）", req.Method, req.URL),
		}
		if opts.ShowRequest {
			res.RequestPreview = previewRequest(req)
		}
		return res, nil
	}

	// 3) Execute.
	start := e.Now()
	resp, body, err := e.send(ctx, req, opts.ExtraHeaders)
	elapsed := e.Now().Sub(start)
	if err != nil {
		return nil, err
	}
	res := &ExecutionResult{
		ProfileID: p.Metadata.ID, ProfileVer: p.Metadata.Version, ProfileHash: p.SHA256,
		Action: opts.Action, Operation: action.Operation, Host: mustHost(req.URL),
		Status: resp.StatusCode, Duration: elapsed,
	}
	res.RequestID = extractRequestID(body)

	okStatus := resp.StatusCode >= 200 && resp.StatusCode < 300
	if action.SuccessWhen != nil && action.SuccessWhen.Status != 0 {
		okStatus = resp.StatusCode == action.SuccessWhen.Status
	}
	if !okStatus {
		code := extractErrorCode(body)
		for _, dup := range successDuplicateCodes(action) {
			if strings.EqualFold(code, dup) {
				res.AlreadyDone = true
				res.Message = "规则已存在（" + code + "）"
				return res, nil
			}
		}
		return res, fmt.Errorf("%s %s failed: HTTP %d: %s", action.Operation, res.Host, resp.StatusCode, snippet(body))
	}
	res.Message = "已放行端口"
	if opts.Action == ActionRevokePortAllowed {
		res.Message = "已回收端口"
	}
	return res, nil
}

func successDuplicateCodes(a Action) []string {
	if a.SuccessWhen == nil {
		return nil
	}
	return a.SuccessWhen.DuplicateCodes
}

// buildData merges profile defaults with caller params.
func (e *Engine) buildData(p *Profile, params map[string]any) (map[string]any, error) {
	data := map[string]any{}
	for _, def := range p.Spec.Parameters {
		if def.Default != "" {
			data[def.Name] = def.Default
		}
	}
	for k, v := range params {
		data[k] = v
	}
	for _, def := range p.Spec.Parameters {
		if !def.Required {
			continue
		}
		v, ok := data[def.Name]
		if !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
			return nil, fmt.Errorf("缺少必填参数 %s（%s）", def.Name, def.Label)
		}
	}
	return data, nil
}

// buildRequest renders endpoint + params into a Request.
func (e *Engine) buildRequest(p *Profile, action Action, data map[string]any, actionName string) (*Request, error) {
	rawURL, err := Render(p.Spec.Endpoint.Template, data)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for k, v := range p.Spec.Endpoint.Query {
		rv, err := Render(v, data)
		if err != nil {
			return nil, err
		}
		q.Set(k, rv)
	}
	rendered, err := RenderParams(action.Params, data)
	if err != nil {
		return nil, err
	}
	method := strings.ToUpper(p.Spec.Endpoint.Method)
	// An action may override the method: Azure creates NSG rules with PUT but
	// deletes them with DELETE, and one endpoint method cannot express both.
	if m := strings.ToUpper(strings.TrimSpace(action.Method)); m != "" {
		method = m
	}
	if method == "" {
		method = http.MethodPost
	}
	req := &Request{
		Method:    method,
		Headers:   map[string]string{},
		Operation: action.Operation,
	}
	for k, v := range p.Spec.Endpoint.Headers {
		rv, err := Render(v, data)
		if err != nil {
			return nil, err
		}
		req.Headers[k] = rv
	}

	ct := p.Spec.Endpoint.ContentType
	switch {
	case strings.Contains(ct, "json"):
		payload := any(rendered)
		if action.Body != nil {
			payload = wrapBody(rendered, action.Body)
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req.Body = b
		req.Headers["Content-Type"] = ct
	case strings.Contains(ct, "x-www-form-urlencoded"):
		form := url.Values{}
		// RPC-style providers (Aliyun, AWS query API) require the operation
		// name as a parameter; profiles should not have to repeat it.
		if _, ok := rendered["Action"]; !ok {
			form.Set("Action", action.Operation)
		}
		for k, v := range rendered {
			form.Set(k, fmt.Sprint(v))
		}
		for k, v := range q {
			form.Set(k, v[0])
		}
		req.FormBody = map[string]string{}
		for k := range form {
			req.FormBody[k] = form.Get(k)
		}
		req.Headers["Content-Type"] = ct
	default:
		// JSON is the sane default for REST providers.
		b, err := json.Marshal(rendered)
		if err != nil {
			return nil, err
		}
		req.Body = b
		if ct != "" {
			req.Headers["Content-Type"] = ct
		}
	}
	if len(q) > 0 && req.FormBody == nil {
		rawURL += "?" + q.Encode()
	}
	req.URL = rawURL
	return req, nil
}

func wrapBody(params map[string]any, spec *BodySpec) any {
	if spec.Envelope != "" {
		return map[string]any{spec.Envelope: params}
	}
	if spec.ArrayField != "" {
		list := []any{}
		for k, v := range params {
			list = append(list, map[string]any{k: v})
		}
		return map[string]any{spec.ArrayField: list}
	}
	return params
}

// probe runs the read-only idempotency check.
func (e *Engine) probe(ctx context.Context, p *Profile, check *IdempotentCheck, data map[string]any, opts RunOptions) (bool, string, error) {
	act := Action{Operation: check.Operation, Params: check.Params}
	req, err := e.buildRequest(p, act, data, ActionEnsurePortAllowed)
	if err != nil {
		return false, "", err
	}
	if err := e.checkHost(p, req.URL); err != nil {
		return false, "", err
	}
	signer := e.signers[p.Spec.Auth.Engine]
	if err := signer.Sign(ctx, req, opts.Creds); err != nil {
		return false, "", fmt.Errorf("sign: %w", err)
	}
	if opts.DryRun {
		return false, "", nil
	}
	resp, body, err := e.send(ctx, req, opts.ExtraHeaders)
	if err != nil {
		return false, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, "", fmt.Errorf("probe HTTP %d: %s", resp.StatusCode, snippet(body))
	}
	return matchExisting(body, check.Match, data)
}

// matchExisting walks the response and compares fields.
func matchExisting(body []byte, m MatchSpec, data map[string]any) (bool, string, error) {
	if m.Path == "" {
		return false, "", nil
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, "", fmt.Errorf("probe response is not JSON: %w", err)
	}
	node := lookupPath(doc, m.Path)
	list, ok := node.([]any)
	if !ok {
		return false, "", nil
	}
	for _, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if ruleMatches(obj, m, data) {
			return true, fmt.Sprintf("%s 中已存在匹配规则", m.Path), nil
		}
	}
	return false, "", nil
}

func ruleMatches(obj map[string]any, m MatchSpec, data map[string]any) bool {
	for field, tpl := range m.Fields {
		want, err := Render(tpl, data)
		if err != nil {
			return false
		}
		// AWS-style responses split the range into FromPort/ToPort; join them so
		// a single "port" field comparison works for every provider.
		got := fieldValue(obj, field)
		if m.Compare == "port_range" && isPortField(field) {
			if !portRangeEqual(got, want) {
				return false
			}
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(want)) {
			return false
		}
	}
	return true
}

// isPortField reports whether a response field carries a port or port range.
func isPortField(field string) bool {
	f := strings.ToLower(field)
	return strings.Contains(f, "port")
}

// fieldValue reads a field, synthesising "min-max" for AWS FromPort/ToPort.
func fieldValue(obj map[string]any, field string) string {
	if v := lookupPath(obj, field); v != nil {
		return fmt.Sprint(v)
	}
	if strings.EqualFold(field, "FromPort") {
		lo := lookupPath(obj, "FromPort")
		hi := lookupPath(obj, "ToPort")
		if lo != nil && hi != nil {
			return fmt.Sprintf("%v-%v", lo, hi)
		}
	}
	return ""
}

// portRangeEqual accepts the provider's dialect and compares normalised ranges.
func portRangeEqual(got, want string) bool {
	gmin, gmax, gok := parseRange(got)
	wmin, wmax, wok := parseRange(want)
	if !gok || !wok {
		return false
	}
	return gmin == wmin && gmax == wmax
}

func parseRange(s string) (int, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "All" || s == "all" || s == "*" || s == "-1" {
		return 0, 0, false
	}
	sep := "-"
	if strings.Contains(s, "/") {
		sep = "/"
	}
	parts := strings.SplitN(s, sep, 2)
	lo, ok := atoi(parts[0])
	if !ok {
		return 0, 0, false
	}
	if len(parts) == 1 {
		return lo, lo, true
	}
	hi, ok := atoi(parts[1])
	if !ok {
		return 0, 0, false
	}
	return lo, hi, true
}

func atoi(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// lookupPath walks "a.b.c" through nested maps.
func lookupPath(doc any, p string) any {
	cur := doc
	for _, seg := range strings.Split(p, ".") {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[seg]
		case []any:
			// "Permissions.Permission.0" style indices.
			idx, ok := atoi(seg)
			if !ok || idx >= len(node) {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
	}
	return cur
}

// checkHost enforces allowed_hosts so a malicious profile cannot exfiltrate
// credentials to an attacker-controlled endpoint.
func (e *Engine) checkHost(p *Profile, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid endpoint %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("endpoint must use https, got %q", u.Scheme)
	}
	host := u.Hostname()
	for _, pattern := range p.Spec.AllowedHosts {
		if matchHost(pattern, host) {
			return nil
		}
	}
	return fmt.Errorf("host %q is not in allowed_hosts %v", host, p.Spec.AllowedHosts)
}

// matchHost supports a leading "*." wildcard and exact matches.
func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	host = strings.ToLower(host)
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix) && len(host) > len(suffix)
	}
	// Middle wildcard such as ecs.*.aliyuncs.com
	if strings.Contains(pattern, "*") {
		parts := strings.SplitN(pattern, "*", 2)
		if !strings.HasPrefix(host, parts[0]) {
			return false
		}
		rest := parts[1]
		if strings.HasPrefix(rest, ".") {
			return strings.HasSuffix(host, rest) && len(host) > len(parts[0])+len(rest)
		}
		return strings.HasSuffix(host, rest)
	}
	return false
}

// send performs the HTTP request.
func (e *Engine) send(ctx context.Context, req *Request, extra map[string]string) (*http.Response, []byte, error) {
	var body io.Reader
	switch {
	case req.FormBody != nil:
		form := url.Values{}
		for k, v := range req.FormBody {
			form.Set(k, v)
		}
		body = strings.NewReader(form.Encode())
	case len(req.Body) > 0:
		body = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, body)
	if err != nil {
		return nil, nil, err
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range extra {
		httpReq.Header.Set(k, v)
	}
	if req.FormBody != nil && httpReq.Header.Get("Content-Type") == "" {
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("request to %s failed: %w", mustHost(req.URL), err)
	}
	defer resp.Body.Close()
	limit := e.MaxResponseBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, nil, err
	}
	return resp, raw, nil
}

func mustHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

// extractErrorCode pulls a provider error code from common envelopes.
func extractErrorCode(body []byte) string {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return ""
	}
	for _, key := range []string{"Code", "code", "errorCode", "error"} {
		if v, ok := doc[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			if m, ok := v.(map[string]any); ok {
				if s, ok := m["code"].(string); ok {
					return s
				}
			}
		}
	}
	if resp, ok := doc["Response"].(map[string]any); ok {
		if e, ok := resp["Error"].(map[string]any); ok {
			if s, ok := e["Code"].(string); ok {
				return s
			}
		}
	}
	return ""
}

func extractRequestID(body []byte) string {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return ""
	}
	for _, key := range []string{"RequestId", "requestId", "request_id"} {
		if s, ok := doc[key].(string); ok {
			return s
		}
	}
	if resp, ok := doc["Response"].(map[string]any); ok {
		if s, ok := resp["RequestId"].(string); ok {
			return s
		}
	}
	return ""
}

// ErrNotPermitted is returned when a profile declares permissions the caller
// has not acknowledged.
var ErrNotPermitted = errors.New("profile permissions not acknowledged")

// EnsurePath joins URL path segments safely.
func EnsurePath(base string, segs ...string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.Path = path.Join(append([]string{u.Path}, segs...)...)
	return u.String()
}

// sensitiveHeaders are replaced in a preview so a dry-run can be shared safely.
var sensitiveHeaders = map[string]bool{
	"authorization":        true,
	"x-amz-security-token": true,
	"x-amz-signature":      true,
	"proxy-authorization":  true,
	"x-auth-token":         true,
	"cookie":               true,
}

// previewRequest describes a rendered request without its credentials.
//
// A dry-run used to report only the URL, which is not enough to confirm that a
// provider-specific body (Azure's properties block, GCP's allowed[].ports) is
// shaped correctly. This lets an operator check the exact call before any
// cloud API is touched, while redacting anything that carries a secret.
func previewRequest(req *Request) *RequestPreview {
	pv := &RequestPreview{Method: req.Method, URL: req.URL}
	if len(req.Headers) > 0 {
		pv.Headers = make(map[string]string, len(req.Headers))
		for k, v := range req.Headers {
			if sensitiveHeaders[strings.ToLower(k)] {
				pv.Headers[k] = "<redacted>"
				continue
			}
			pv.Headers[k] = v
		}
	}
	switch {
	case len(req.Body) > 0:
		pv.Body = string(req.Body)
	case len(req.FormBody) > 0:
		// RPC providers send form-encoded bodies; render them for inspection.
		parts := make([]string, 0, len(req.FormBody))
		for k, v := range req.FormBody {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
		sort.Strings(parts)
		pv.Body = strings.Join(parts, "&")
	}
	return pv
}
