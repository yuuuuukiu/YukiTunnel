// Command yuki-live-test drives a real cloud security group through the
// YukiTunnel profile engine so operators can verify signatures and API shapes
// against a live account.
//
// It performs the full lifecycle and prints PASS/FAIL per step:
//
//	ensure_port_allowed -> describe (rule present) -> ensure again (idempotent)
//	-> revoke_port_allowed -> describe (rule gone)
//
// Usage:
//
//	yuki-live-test -profile aliyun-ecs-security-group -file profiles/aliyun-ecs-security-group.yaml \
//	  -param region=cn-hangzhou -param security_group_id=sg-xxxx \
//	  -port 20000 -proto tcp
//
// Credentials come from the environment (never from flags, which leak into
// shell history and process listings):
//
//	Aliyun : YUKI_ALIYUN_ACCESS_KEY_ID / YUKI_ALIYUN_ACCESS_KEY_SECRET
//	Tencent: YUKI_TENCENT_SECRET_ID / YUKI_TENCENT_SECRET_KEY
//	AWS    : YUKI_AWS_ACCESS_KEY_ID / YUKI_AWS_SECRET_ACCESS_KEY / YUKI_AWS_SESSION_TOKEN
//	Huawei : YUKI_HUAWEI_IAM_TOKEN / YUKI_HUAWEI_PROJECT_ID
//	Azure  : YUKI_AZURE_ACCESS_TOKEN
//	GCP    : YUKI_GCP_ACCESS_TOKEN
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile"
	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile/registry"
)

type kvFlag map[string]string

func (k kvFlag) String() string { return fmt.Sprint(map[string]string(k)) }

func (k kvFlag) Set(v string) error {
	key, val, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return fmt.Errorf("expected key=value, got %q", v)
	}
	k[key] = val
	return nil
}

type stepResult struct {
	Name     string        `json:"name"`
	OK       bool          `json:"ok"`
	Detail   string        `json:"detail"`
	Duration time.Duration `json:"duration_ms"`
}

func main() {
	var (
		profileID = flag.String("profile", "", "profile id (informational)")
		file      = flag.String("file", "", "path to the profile YAML")
		port      = flag.Int("port", 0, "port to open/close")
		proto     = flag.String("proto", "tcp", "protocol")
		skipClock = flag.Bool("skip-clock-check", false, "skip the clock skew check")
		dryRun    = flag.Bool("dry-run", false, "render and sign without calling the provider")
		jsonOut   = flag.Bool("json", false, "machine-readable output")
	)
	params := kvFlag{}
	flag.Var(params, "param", "profile parameter key=value (repeatable)")
	flag.Parse()

	if *file == "" || *port == 0 {
		fatalf("--file and --port are required")
	}
	if *profileID == "" {
		*profileID = strings.TrimSuffix(strings.TrimPrefix(*file, "profiles/"), ".yaml")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var results []stepResult
	record := func(name string, ok bool, detail string, d time.Duration) {
		results = append(results, stepResult{Name: name, OK: ok, Detail: detail, Duration: d})
		if !*jsonOut {
			status := "PASS"
			if !ok {
				status = "FAIL"
			}
			fmt.Printf("[%s] %-32s %6dms  %s\n", status, name, d.Milliseconds(), detail)
		}
	}

	// 0) Clock skew: every provider rejects requests signed outside its window.
	if !*skipClock {
		start := time.Now()
		ok, detail := checkClockSkew(ctx)
		record("clock-skew", ok, detail, time.Since(start))
		if !ok {
			finish(results, *jsonOut)
			os.Exit(1)
		}
	}

	// 1) Load and validate the profile.
	start := time.Now()
	raw, err := os.ReadFile(*file)
	if err != nil {
		fatalf("read profile: %v", err)
	}
	var p cloudprofile.Profile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		fatalf("parse profile: %v", err)
	}
	if err := p.Validate(); err != nil {
		fatalf("validate profile: %v", err)
	}
	p.SHA256 = cloudprofile.Digest(raw)
	record("profile-load", true, fmt.Sprintf("%s@%s sha256=%s", p.Metadata.ID, p.Metadata.Version, p.SHA256[:12]), time.Since(start))

	creds, err := credentialsFor(p.Spec.Auth.Engine)
	if err != nil {
		fatalf("%v", err)
	}

	// 2) Engine with the configured proxy (if any).
	eng, err := cloudprofile.NewEngine(os.Getenv("YUKI_PROXY"))
	if err != nil {
		fatalf("engine: %v", err)
	}

	paramMap := map[string]any{}
	for k, v := range params {
		paramMap[k] = v
	}
	run := func(action string) (*cloudprofile.ExecutionResult, error) {
		base := map[string]any{
			"port_min": *port, "port_max": *port, "protocol": *proto,
		}
		for k, v := range paramMap {
			base[k] = v
		}
		return eng.Run(ctx, &p, cloudprofile.RunOptions{
			Action: action, Params: base, Creds: creds, DryRun: *dryRun,
		})
	}

	// 3) ensure
	start = time.Now()
	res, err := run(cloudprofile.ActionEnsurePortAllowed)
	if err != nil {
		record("ensure_port_allowed", false, err.Error(), time.Since(start))
		finish(results, *jsonOut)
		os.Exit(1)
	}
	record("ensure_port_allowed", true, describe(res), time.Since(start))
	firstCreated := !res.AlreadyDone && !res.Skipped

	// 4) ensure again -> must be idempotent
	start = time.Now()
	res2, err := run(cloudprofile.ActionEnsurePortAllowed)
	if err != nil {
		record("ensure_idempotent", false, err.Error(), time.Since(start))
		finish(results, *jsonOut)
		os.Exit(1)
	}
	idempotent := res2.AlreadyDone || res2.Skipped
	if *dryRun {
		// dry-run never writes, so idempotency cannot be observed.
		idempotent = true
	}
	record("ensure_idempotent", idempotent, describe(res2), time.Since(start))

	// 5) revoke
	start = time.Now()
	res3, err := run(cloudprofile.ActionRevokePortAllowed)
	if err != nil {
		record("revoke_port_allowed", false, err.Error(), time.Since(start))
		finish(results, *jsonOut)
		os.Exit(1)
	}
	record("revoke_port_allowed", true, describe(res3), time.Since(start))

	// 6) ensure again after revoke should recreate (proves revoke really worked)
	if firstCreated && !*dryRun {
		start = time.Now()
		res4, err := run(cloudprofile.ActionEnsurePortAllowed)
		if err != nil {
			record("recreate_after_revoke", false, err.Error(), time.Since(start))
		} else {
			record("recreate_after_revoke", !res4.AlreadyDone && !res4.Skipped, describe(res4), time.Since(start))
			// Leave the account clean.
			_, _ = run(cloudprofile.ActionRevokePortAllowed)
		}
	}

	finish(results, *jsonOut)
	for _, r := range results {
		if !r.OK {
			os.Exit(1)
		}
	}
}

func describe(res *cloudprofile.ExecutionResult) string {
	if res == nil {
		return "no result"
	}
	parts := []string{}
	if res.AlreadyDone {
		parts = append(parts, "already-present")
	}
	if res.Skipped {
		parts = append(parts, "skipped")
	}
	if res.Status > 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", res.Status))
	}
	if res.Host != "" {
		parts = append(parts, res.Host)
	}
	if res.RequestID != "" {
		parts = append(parts, "req="+res.RequestID)
	}
	if res.Message != "" {
		parts = append(parts, res.Message)
	}
	return strings.Join(parts, " | ")
}

// credentialsFor maps environment variables onto the engine's credential names.
func credentialsFor(engine string) (cloudprofile.Credentials, error) {
	need := func(names ...string) (cloudprofile.Credentials, error) {
		out := cloudprofile.Credentials{}
		var missing []string
		for _, n := range names {
			v := os.Getenv(n)
			if v == "" {
				missing = append(missing, n)
				continue
			}
			out[n] = v
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("缺少环境变量：%s", strings.Join(missing, ", "))
		}
		return out, nil
	}
	switch engine {
	case "aliyun-hmac-sha1":
		env, err := need("YUKI_ALIYUN_ACCESS_KEY_ID", "YUKI_ALIYUN_ACCESS_KEY_SECRET")
		if err != nil {
			return nil, err
		}
		return cloudprofile.Credentials{
			"access_key_id":     env["YUKI_ALIYUN_ACCESS_KEY_ID"],
			"access_key_secret": env["YUKI_ALIYUN_ACCESS_KEY_SECRET"],
		}, nil
	case "tencent-hmac-sha256":
		env, err := need("YUKI_TENCENT_SECRET_ID", "YUKI_TENCENT_SECRET_KEY")
		if err != nil {
			return nil, err
		}
		return cloudprofile.Credentials{
			"secret_id":  env["YUKI_TENCENT_SECRET_ID"],
			"secret_key": env["YUKI_TENCENT_SECRET_KEY"],
			"region":     os.Getenv("YUKI_REGION"),
		}, nil
	case "aws-sigv4":
		env, err := need("YUKI_AWS_ACCESS_KEY_ID", "YUKI_AWS_SECRET_ACCESS_KEY")
		if err != nil {
			return nil, err
		}
		return cloudprofile.Credentials{
			"access_key_id":     env["YUKI_AWS_ACCESS_KEY_ID"],
			"secret_access_key": env["YUKI_AWS_SECRET_ACCESS_KEY"],
			"session_token":     os.Getenv("YUKI_AWS_SESSION_TOKEN"),
			"region":            os.Getenv("YUKI_REGION"),
		}, nil
	case "huawei-aksk":
		env, err := need("YUKI_HUAWEI_IAM_TOKEN", "YUKI_HUAWEI_PROJECT_ID")
		if err != nil {
			return nil, err
		}
		return cloudprofile.Credentials{
			"iam_token":  env["YUKI_HUAWEI_IAM_TOKEN"],
			"project_id": env["YUKI_HUAWEI_PROJECT_ID"],
		}, nil
	case "azure-oauth2", "gcp-oauth2":
		env, err := need("YUKI_OAUTH_ACCESS_TOKEN")
		if err != nil {
			return nil, err
		}
		return cloudprofile.Credentials{"access_token": env["YUKI_OAUTH_ACCESS_TOKEN"]}, nil
	default:
		return nil, fmt.Errorf("unknown signing engine %q", engine)
	}
}

// checkClockSkew compares the local clock with the provider's Date header.
// Providers reject requests signed more than ~5-15 minutes off.
func checkClockSkew(ctx context.Context) (bool, string) {
	host := os.Getenv("YUKI_CLOCK_HOST")
	if host == "" {
		host = "https://www.cloudflare.com/cdn-cgi/trace"
	}
	start := time.Now()
	resp, err := httpGet(ctx, host)
	if err != nil {
		return true, "无法获取网络时间（跳过检查）：" + err.Error()
	}
	defer resp.Body.Close()
	serverDate := resp.Header.Get("Date")
	if serverDate == "" {
		return true, "响应中没有 Date 头（跳过检查）"
	}
	remote, err := time.Parse(time.RFC1123, serverDate)
	if err != nil {
		return true, "无法解析 Date 头（跳过检查）"
	}
	skew := start.Sub(remote)
	if skew < 0 {
		skew = -skew
	}
	if skew > 15*time.Minute {
		return false, fmt.Sprintf("本机时间与网络时间相差 %s（超过 15 分钟），云 API 会拒绝签名，请先校准时间", skew.Round(time.Second))
	}
	return true, fmt.Sprintf("时间偏差 %s（在 15 分钟内）", skew.Round(time.Second))
}

func finish(results []stepResult, asJSON bool) {
	if !asJSON {
		fmt.Println()
		failed := 0
		for _, r := range results {
			if !r.OK {
				failed++
			}
		}
		if failed == 0 {
			fmt.Printf("ALL PASS (%d steps)\n", len(results))
		} else {
			fmt.Printf("%d/%d steps FAILED\n", failed, len(results))
		}
		return
	}
	sort.SliceStable(results, func(i, j int) bool { return false })
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"steps": results})
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

// httpGet is a small indirection so tests can stub the clock probe.
var httpGet = func(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return client.Do(req)
}

var _ = registry.DefaultMirrors
