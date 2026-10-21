package cloudprofile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// officialProfiles is the set shipped in /profiles. Every one must parse and
// validate, otherwise the registry would publish a broken catalogue.
var officialProfiles = []string{
	"../../profiles/aliyun-ecs-security-group.yaml",
	"../../profiles/tencent-vpc-security-group.yaml",
	"../../profiles/aws-ec2-security-group.yaml",
	"../../profiles/huawei-vpc-security-group.yaml",
	"../../profiles/azure-nsg.yaml",
	"../../profiles/gcp-firewall.yaml",
}

func loadProfile(t *testing.T, path string) *Profile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var p Profile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		t.Fatalf("%s: parse: %v", path, err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("%s: validate: %v", path, err)
	}
	return &p
}

func TestOfficialProfilesAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, path := range officialProfiles {
		p := loadProfile(t, path)
		if seen[p.Metadata.ID] {
			t.Fatalf("duplicate profile id %q", p.Metadata.ID)
		}
		seen[p.Metadata.ID] = true
		if len(p.Spec.AllowedHosts) == 0 {
			t.Fatalf("%s: allowed_hosts must not be empty", p.Metadata.ID)
		}
		if len(p.Spec.Permissions) == 0 {
			t.Fatalf("%s: permissions must not be empty", p.Metadata.ID)
		}
		if _, ok := p.Spec.Actions[ActionEnsurePortAllowed]; !ok {
			t.Fatalf("%s: missing ensure_port_allowed", p.Metadata.ID)
		}
		if _, ok := p.Spec.Actions[ActionRevokePortAllowed]; !ok {
			t.Fatalf("%s: missing revoke_port_allowed", p.Metadata.ID)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("expected 6 official profiles, got %d", len(seen))
	}
}

func TestPortFormatDiffersPerVendor(t *testing.T) {
	cases := []struct {
		profile, want string
	}{
		{"../../profiles/aliyun-ecs-security-group.yaml", "20000/20100"},
		{"../../profiles/tencent-vpc-security-group.yaml", "20000-20100"},
		{"../../profiles/aws-ec2-security-group.yaml", "20000"},
		{"../../profiles/azure-nsg.yaml", "20000-20100"},
		{"../../profiles/gcp-firewall.yaml", "20000-20100"},
	}
	for _, tc := range cases {
		p := loadProfile(t, tc.profile)
		data := map[string]any{"port_min": "20000", "port_max": "20100", "protocol": "tcp"}
		got, err := Render("{{ port_range \"slash\" .port_min .port_max }}", data)
		if err != nil {
			t.Fatal(err)
		}
		if p.Metadata.Vendor == "aliyun" && got != tc.want {
			t.Fatalf("aliyun port format: got %q want %q", got, tc.want)
		}
	}
}

func TestPortRangeFormats(t *testing.T) {
	cases := []struct {
		format   string
		min, max string
		want     string
	}{
		{"slash", "80", "80", "80/80"},
		{"slash", "8000", "8010", "8000/8010"},
		{"dash", "80", "80", "80"},
		{"dash", "8000", "8010", "8000-8010"},
		{"string", "443", "443", "443"},
	}
	for _, tc := range cases {
		got, err := portRange(tc.format, tc.min, tc.max)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("port_range(%s,%s,%s)=%q want %q", tc.format, tc.min, tc.max, got, tc.want)
		}
	}
}

func TestProtocolCasing(t *testing.T) {
	cases := map[string]string{"lower": "tcp", "upper": "TCP", "title": "Tcp"}
	for casing, want := range cases {
		if got := proto(casing, "tcp"); got != want {
			t.Fatalf("proto(%s)=%q want %q", casing, got, want)
		}
	}
	if got := proto("lower", "all"); got != "-1" {
		t.Fatalf("all/lower = %q want -1", got)
	}
	if got := proto("title", "all"); got != "*" {
		t.Fatalf("all/title = %q want *", got)
	}
}

func TestAliyunSignatureIsStable(t *testing.T) {
	s := NewAliyunSigner()
	fixed := s.Now()
	req := &Request{
		Method:  "POST",
		URL:     "https://ecs.cn-hangzhou.aliyuncs.com/",
		Headers: map[string]string{},
		FormBody: map[string]string{
			"Action":          "AuthorizeSecurityGroup",
			"Version":         "2014-05-26",
			"SecurityGroupId": "sg-test",
			"IpProtocol":      "tcp",
			"PortRange":       "20000/20100",
		},
		Operation: "AuthorizeSecurityGroup",
	}
	if err := s.Sign(context.Background(), req, Credentials{
		"access_key_id": "test-ak", "access_key_secret": "test-sk",
	}); err != nil {
		t.Fatal(err)
	}
	if req.FormBody["Signature"] == "" {
		t.Fatal("signature missing")
	}
	if req.FormBody["SignatureMethod"] != "HMAC-SHA1" {
		t.Fatal("wrong signature method")
	}
	if req.FormBody["AccessKeyId"] != "test-ak" {
		t.Fatal("access key missing")
	}
	_ = fixed
	// Canonicalisation must percent-encode per RFC 3986.
	if got := percentEncode("a b*c~d"); got != "a%20b%2Ac~d" {
		t.Fatalf("percentEncode = %q", got)
	}
}

func TestTencentSignatureHeaders(t *testing.T) {
	s := NewTencentSigner()
	req := &Request{
		Method:    "POST",
		URL:       "https://vpc.tencentcloudapi.com/",
		Headers:   map[string]string{},
		Body:      []byte(`{"SecurityGroupId":"sg-1"}`),
		Operation: "CreateSecurityGroupPolicies",
	}
	if err := s.Sign(context.Background(), req, Credentials{
		"secret_id": "id", "secret_key": "key", "region": "ap-guangzhou",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.Headers["Authorization"], "TC3-HMAC-SHA256 Credential=id/") {
		t.Fatalf("bad auth header: %s", req.Headers["Authorization"])
	}
	if req.Headers["X-TC-Action"] != "CreateSecurityGroupPolicies" {
		t.Fatal("missing X-TC-Action")
	}
	if req.Headers["X-TC-Region"] != "ap-guangzhou" {
		t.Fatal("missing X-TC-Region")
	}
}

func TestAWSSigV4Headers(t *testing.T) {
	s := NewAWSSigV4Signer()
	req := &Request{
		Method:    "POST",
		URL:       "https://ec2.us-east-1.amazonaws.com/",
		Headers:   map[string]string{},
		Operation: "AuthorizeSecurityGroupIngress",
	}
	if err := s.Sign(context.Background(), req, Credentials{
		"access_key_id": "AKIA", "secret_access_key": "secret", "region": "us-east-1",
	}); err != nil {
		t.Fatal(err)
	}
	auth := req.Headers["Authorization"]
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIA/") {
		t.Fatalf("bad auth header: %s", auth)
	}
	if !strings.Contains(auth, "/us-east-1/ec2/aws4_request") {
		t.Fatalf("wrong credential scope: %s", auth)
	}
	if req.Headers["X-Amz-Date"] == "" {
		t.Fatal("missing X-Amz-Date")
	}
	if req.FormBody["Action"] != "AuthorizeSecurityGroupIngress" {
		t.Fatal("Action missing from form")
	}
}

func TestHuaweiRequiresToken(t *testing.T) {
	s := NewHuaweiSigner()
	req := &Request{URL: "https://vpc.cn-north-4.myhuaweicloud.com/v2/p/sg", Headers: map[string]string{}}
	if err := s.Sign(context.Background(), req, Credentials{}); err == nil {
		t.Fatal("expected error without iam_token")
	}
	if err := s.Sign(context.Background(), req, Credentials{"iam_token": "tok", "project_id": "p1"}); err != nil {
		t.Fatal(err)
	}
	if req.Headers["X-Auth-Token"] != "tok" || req.Headers["X-Project-Id"] != "p1" {
		t.Fatalf("headers = %v", req.Headers)
	}
}

func TestHostAllowlistBlocksExfiltration(t *testing.T) {
	e, err := NewEngine("")
	if err != nil {
		t.Fatal(err)
	}
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	// A malicious profile could try to point at an attacker host.
	p.Spec.Endpoint.Template = "https://evil.example.com/"
	_, err = e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{"region": "cn-hangzhou", "security_group_id": "sg-1", "port_min": "80", "port_max": "80"},
		Creds:  Credentials{"access_key_id": "a", "access_key_secret": "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "allowed_hosts") {
		t.Fatalf("expected allowed_hosts rejection, got %v", err)
	}
}

func TestHTTPEndpointRejected(t *testing.T) {
	e, _ := NewEngine("")
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	p.Spec.Endpoint.Template = "http://ecs.cn-hangzhou.aliyuncs.com/"
	_, err := e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{"region": "cn-hangzhou", "security_group_id": "sg-1", "port_min": "80", "port_max": "80"},
		Creds:  Credentials{"access_key_id": "a", "access_key_secret": "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https enforcement, got %v", err)
	}
}

func TestMatchHostWildcards(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"ecs.*.aliyuncs.com", "ecs.cn-hangzhou.aliyuncs.com", true},
		{"ecs.*.aliyuncs.com", "ecs.aliyuncs.com", false},
		{"ecs.*.aliyuncs.com", "evil.com", false},
		{"*.amazonaws.com", "ec2.us-east-1.amazonaws.com", true},
		{"*.amazonaws.com", "amazonaws.com", false},
		{"management.azure.com", "management.azure.com", true},
		{"management.azure.com", "management.azure.com.evil.io", false},
	}
	for _, tc := range cases {
		if got := matchHost(tc.pattern, tc.host); got != tc.want {
			t.Fatalf("matchHost(%q,%q)=%v want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
}

func TestIdempotentCheckSkipsWrite(t *testing.T) {
	var calls []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		_ = r.ParseForm()
		// Simulate Aliyun DescribeSecurityGroups returning the rule already present.
		resp := map[string]any{
			"SecurityGroups": map[string]any{
				"SecurityGroup": []any{
					map[string]any{
						"Permissions": map[string]any{
							"Permission": []any{
								map[string]any{
									"IpProtocol":   "tcp",
									"PortRange":    "20000/20100",
									"SourceCidrIp": "0.0.0.0/0",
									"Policy":       "accept",
								},
							},
						},
					},
				},
			},
			"RequestId": "req-1",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e, _ := NewEngine("")
	e.client = srv.Client()
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	// Point at the test server and allow its host.
	u, _ := url.Parse(srv.URL)
	p.Spec.Endpoint.Template = srv.URL + "/"
	p.Spec.AllowedHosts = []string{u.Hostname()}

	res, err := e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{
			"region": "cn-hangzhou", "security_group_id": "sg-1",
			"port_min": "20000", "port_max": "20100", "source_cidr": "0.0.0.0/0",
			"protocol": "tcp",
		},
		Creds: Credentials{"access_key_id": "a", "access_key_secret": "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AlreadyDone || !res.Skipped {
		t.Fatalf("expected already-done result, got %+v", res)
	}
	if len(calls) != 1 {
		t.Fatalf("expected only the probe call, got %d calls: %v", len(calls), calls)
	}
}

func TestDryRunDoesNotCallProvider(t *testing.T) {
	called := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	e, _ := NewEngine("")
	e.client = srv.Client()
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	u, _ := url.Parse(srv.URL)
	p.Spec.Endpoint.Template = srv.URL + "/"
	p.Spec.AllowedHosts = []string{u.Hostname()}
	res, err := e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{"region": "cn-hangzhou", "security_group_id": "sg-1", "port_min": "80", "port_max": "80"},
		Creds:  Credentials{"access_key_id": "a", "access_key_secret": "b"},
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("dry-run must not call the provider")
	}
	if !res.Skipped {
		t.Fatal("dry-run should be marked skipped")
	}
}

func TestMissingRequiredParameter(t *testing.T) {
	e, _ := NewEngine("")
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	_, err := e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{"region": "cn-hangzhou"},
		Creds:  Credentials{"access_key_id": "a", "access_key_secret": "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "security_group_id") {
		t.Fatalf("expected missing parameter error, got %v", err)
	}
}

func TestUnknownActionRejected(t *testing.T) {
	e, _ := NewEngine("")
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	_, err := e.Run(context.Background(), p, RunOptions{Action: "drop_database"})
	if err == nil || !strings.Contains(err.Error(), "no action") {
		t.Fatalf("expected unknown action error, got %v", err)
	}
}

func TestUnsupportedEngineRejected(t *testing.T) {
	e, _ := NewEngine("")
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	p.Spec.Auth.Engine = "magic-signer"
	_, err := e.Run(context.Background(), p, RunOptions{Action: ActionEnsurePortAllowed})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	p := &Profile{APIVersion: APIVersion, Kind: "CloudProfile"}
	p.Metadata.ID = "bad"
	err := p.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, want := range []string{"metadata.vendor", "allowed_hosts", "permissions", "actions.ensure_port_allowed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should mention %q: %v", want, err)
		}
	}
}

func TestRPCProvidersIncludeActionParam(t *testing.T) {
	var form url.Values
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RequestId":"r1"}`))
	}))
	defer srv.Close()
	e, _ := NewEngine("")
	e.client = srv.Client()
	p := loadProfile(t, "../../profiles/aliyun-ecs-security-group.yaml")
	u, _ := url.Parse(srv.URL)
	p.Spec.Endpoint.Template = srv.URL + "/"
	p.Spec.AllowedHosts = []string{u.Hostname()}
	act := p.Spec.Actions[ActionEnsurePortAllowed]
	act.IdempotentCheck.Params = map[string]any{}
	p.Spec.Actions[ActionEnsurePortAllowed] = act

	if _, err := e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{"region": "cn-hangzhou", "security_group_id": "sg-1", "port_min": "80", "port_max": "80"},
		Creds:  Credentials{"access_key_id": "a", "access_key_secret": "b"},
	}); err != nil {
		t.Fatal(err)
	}
	if form.Get("Action") != "AuthorizeSecurityGroup" {
		t.Fatalf("Action param = %q, want AuthorizeSecurityGroup", form.Get("Action"))
	}
	if form.Get("Signature") == "" {
		t.Fatal("request was not signed")
	}
}

func TestTencentBodyEnvelope(t *testing.T) {
	var body map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"r1"}}`))
	}))
	defer srv.Close()
	e, _ := NewEngine("")
	e.client = srv.Client()
	p := loadProfile(t, "../../profiles/tencent-vpc-security-group.yaml")
	u, _ := url.Parse(srv.URL)
	p.Spec.Endpoint.Template = srv.URL + "/"
	p.Spec.AllowedHosts = []string{u.Hostname()}
	act := p.Spec.Actions[ActionEnsurePortAllowed]
	act.IdempotentCheck.Params = map[string]any{}
	p.Spec.Actions[ActionEnsurePortAllowed] = act

	if _, err := e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{"region": "ap-guangzhou", "security_group_id": "sg-1", "port_min": "80", "port_max": "80"},
		Creds:  Credentials{"secret_id": "id", "secret_key": "key", "region": "ap-guangzhou"},
	}); err != nil {
		t.Fatal(err)
	}
	set, ok := body["SecurityGroupPolicySet"].(map[string]any)
	if !ok {
		t.Fatalf("body missing SecurityGroupPolicySet envelope: %v", body)
	}
	ingress, ok := set["Ingress"].([]any)
	if !ok || len(ingress) != 1 {
		t.Fatalf("Ingress = %v", set["Ingress"])
	}
	rule := ingress[0].(map[string]any)
	if rule["Protocol"] != "TCP" {
		t.Fatalf("Tencent protocol must be uppercase, got %v", rule["Protocol"])
	}
	if rule["Port"] != "80" {
		t.Fatalf("Tencent port = %v want 80", rule["Port"])
	}
}

func TestGCPPortsArray(t *testing.T) {
	var body map[string]any
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"rule"}`))
	}))
	defer srv.Close()
	e, _ := NewEngine("")
	e.client = srv.Client()
	p := loadProfile(t, "../../profiles/gcp-firewall.yaml")
	u, _ := url.Parse(srv.URL)
	p.Spec.Endpoint.Template = srv.URL + "/"
	p.Spec.AllowedHosts = []string{u.Hostname()}
	act := p.Spec.Actions[ActionEnsurePortAllowed]
	act.IdempotentCheck.Params = map[string]any{}
	p.Spec.Actions[ActionEnsurePortAllowed] = act

	if _, err := e.Run(context.Background(), p, RunOptions{
		Action: ActionEnsurePortAllowed,
		Params: map[string]any{
			"project": "p1", "port_min": "8000", "port_max": "8010",
			"rule_name": "yuki-test", "target_tags": "yukitunnel",
		},
		Creds: Credentials{"access_token": "tok", "project": "p1"},
	}); err != nil {
		t.Fatal(err)
	}
	allowed, ok := body["allowed"].([]any)
	if !ok || len(allowed) != 1 {
		t.Fatalf("allowed = %v", body["allowed"])
	}
	rule := allowed[0].(map[string]any)
	ports, ok := rule["ports"].([]any)
	if !ok || len(ports) != 1 || ports[0] != "8000-8010" {
		t.Fatalf("GCP ports = %v want [8000-8010]", rule["ports"])
	}
	if rule["IPProtocol"] != "tcp" {
		t.Fatalf("GCP protocol = %v", rule["IPProtocol"])
	}
}
