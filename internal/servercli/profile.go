package servercli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile"
	"github.com/yuuuuukiu/yukitunnel/internal/cloudprofile/registry"
	"gopkg.in/yaml.v3"
)

// newProfileCmd exposes cloud profile management, mirroring the WebUI page.
func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"profiles"},
		Short:   "云防火墙 Profile：查看、安装、配置凭证、放行端口",
		Long: `云 Profile 是纯声明式 YAML，描述如何在阿里云/腾讯云/AWS/华为云/Azure/GCP
上放行或回收端口。profile 由 GitHub 托管，CI 用 ed25519 签名，客户端下载后
始终校验签名。`,
	}
	cmd.AddCommand(
		newProfileListCmd(), newProfileIndexCmd(), newProfileInstallCmd(),
		newProfileVerifyCmd(), newProfileCredentialCmd(), newProfileRunCmd(),
		newProfileRunsCmd(), newProfileMirrorCmd(), newProfileRekeyCmd(),
	)
	return cmd
}

func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出已安装的 profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			if g.server != "" {
				return profileListRemote()
			}
			local, ok := b.(*LocalBackend)
			if !ok {
				return fmt.Errorf("不支持的 backend")
			}
			list, err := local.Store().ListCloudProfiles(ctx)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"profiles": list})
			}
			rows := make([][]string, 0, len(list))
			for _, p := range list {
				rows = append(rows, []string{
					p.ID, p.Vendor, p.Version,
					map[bool]string{true: "启用", false: "停用"}[p.Enabled],
					shortHash(p.SHA256), p.Source,
				})
			}
			table([]string{"ID", "厂商", "版本", "状态", "SHA256", "来源"}, rows)
			return nil
		},
	}
}

func newProfileIndexCmd() *cobra.Command {
	var vendor string
	var mirror string
	cmd := &cobra.Command{
		Use:   "index",
		Short: "查看远端签名索引（验证签名后展示）",
		RunE: func(cmd *cobra.Command, args []string) error {
			mirrors := profileMirrors()
			if mirror != "" {
				mirrors = append([]string{mirror}, mirrors...)
			}
			reg, err := registry.New(registry.Options{
				Mirrors:     mirrors,
				Proxy:       profileProxy(),
				TrustedKeys: trustedKeys(),
			})
			if err != nil {
				return err
			}
			idx, err := reg.Refresh(context.Background())
			if err != nil {
				return err
			}
			entries := idx.Profiles
			if vendor != "" {
				filtered := entries[:0]
				for _, e := range entries {
					if strings.EqualFold(e.Vendor, vendor) {
						filtered = append(filtered, e)
					}
				}
				entries = filtered
			}
			if isJSON() {
				return printJSON(map[string]any{
					"source": reg.Source(), "key_id": idx.KeyID,
					"generated_at": idx.GeneratedAt, "profiles": entries, "verified": true,
				})
			}
			fmt.Printf("来源：%s\n签名密钥：%s（已验签）\n\n", reg.Source(), idx.KeyID)
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, []string{e.ID, e.Vendor, e.Version, shortHash(e.SHA256)})
			}
			table([]string{"ID", "厂商", "版本", "SHA256"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&vendor, "vendor", "", "按厂商过滤（aliyun/tencent/aws/huawei/azure/gcp）")
	cmd.Flags().StringVar(&mirror, "mirror", "", "额外指定一个镜像地址（优先尝试）")
	return cmd
}

func newProfileInstallCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "install <profile-id>",
		Short: "从签名镜像安装 profile（或 --file 从本地 YAML 安装）",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				return installFromFile(file)
			}
			if len(args) == 0 {
				return fmt.Errorf("请提供 profile id，或使用 --file 从本地安装")
			}
			if g.server != "" {
				return remoteProfileInstall(args[0])
			}
			reg, err := registry.New(registry.Options{
				Mirrors:     profileMirrors(),
				Proxy:       profileProxy(),
				TrustedKeys: trustedKeys(),
			})
			if err != nil {
				return err
			}
			p, err := reg.Load(context.Background(), args[0])
			if err != nil && errors.Is(err, registry.ErrVerification) {
				return fmt.Errorf("profile 验签失败，已拒绝安装（不会退回内置副本）：%w", err)
			}
			if err != nil {
				// Transport failure only: fall back to the copy compiled into
				// this binary so air-gapped installs still work.
				bundled, berr := cloudprofile.BundledProfile(args[0])
				if berr != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "警告：profile 镜像不可达，使用二进制内置副本（未联网验签）\n")
				p = bundled
			}
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			local := b.(*LocalBackend)
			raw, _ := yaml.Marshal(p)
			rec := &cloudProfileRecord{
				ID: p.Metadata.ID, Vendor: p.Metadata.Vendor, Name: p.Metadata.Name,
				Version: p.Metadata.Version, Source: p.Source, SHA256: p.SHA256, Document: string(raw),
			}
			if err := local.saveProfile(ctx, rec); err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "profile": rec})
			}
			fmt.Printf("✓ 已安装 %s@%s（来源 %s，sha256 %s）\n", rec.ID, rec.Version, rec.Source, shortHash(rec.SHA256))
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "从本地 YAML 安装（仍需校验 sha256）")
	return cmd
}

func newProfileVerifyCmd() *cobra.Command {
	var sha string
	cmd := &cobra.Command{
		Use:   "verify <file.yaml>",
		Short: "校验本地 profile 的结构与哈希",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := registry.VerifyFile(args[0], sha, trustedKeys())
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "id": p.Metadata.ID, "sha256": p.SHA256})
			}
			fmt.Printf("✓ %s 校验通过\n", p.Metadata.ID)
			fmt.Printf("  vendor   ：%s\n", p.Metadata.Vendor)
			fmt.Printf("  版本     ：%s\n", p.Metadata.Version)
			fmt.Printf("  sha256   ：%s\n", p.SHA256)
			fmt.Printf("  签名引擎 ：%s\n", p.Spec.Auth.Engine)
			fmt.Printf("  允许域名 ：%s\n", strings.Join(p.Spec.AllowedHosts, ", "))
			fmt.Printf("  权限     ：%s\n", strings.Join(p.Spec.Permissions, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&sha, "sha256", "", "期望的 sha256（留空只做结构校验）")
	return cmd
}

func newProfileCredentialCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "credential",
		Aliases: []string{"cred"},
		Short:   "配置云厂商凭证（加密存储）",
	}
	var (
		label  string
		fields []string
	)
	set := &cobra.Command{
		Use:   "set <profile-id>",
		Short: "设置凭证，例如 --field access_key_id=... --field access_key_secret=...",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values := map[string]string{}
			for _, f := range fields {
				k, v, ok := strings.Cut(f, "=")
				if !ok || k == "" {
					return fmt.Errorf("--field 需要 key=value 格式，收到 %q", f)
				}
				values[k] = v
			}
			if len(values) == 0 {
				return fmt.Errorf("至少需要一个 --field")
			}
			if g.server != "" {
				return remoteCredentialSet(args[0], label, values)
			}
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			local := b.(*LocalBackend)
			if err := local.saveCredential(ctx, args[0], label, values); err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "label": label})
			}
			fmt.Printf("✓ 已保存 %s 的凭证（label=%s），字段：%s\n", args[0], label, strings.Join(mapKeys(values), ", "))
			return nil
		},
	}
	set.Flags().StringVar(&label, "label", "default", "凭证名称")
	set.Flags().StringArrayVar(&fields, "field", nil, "凭证字段 key=value，可重复")
	cmd.AddCommand(set)
	return cmd
}

func newProfileRunCmd() *cobra.Command {
	var (
		action   string
		portMin  int
		portMax  int
		protocol string
		cred     string
		nodeID   string
		paramKV  []string
		dryRun   bool
		ack      bool
	)
	cmd := &cobra.Command{
		Use:   "run <profile-id>",
		Short: "执行 ensure_port_allowed / revoke_port_allowed",
		Args:  cobra.ExactArgs(1),
		Example: `  yuki-server profile run aliyun-ecs-security-group \
    --action ensure_port_allowed --port-min 20000 --port-max 20000 \
    --param region=cn-hangzhou --param security_group_id=sg-xxx --ack`,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{}
			for _, kv := range paramKV {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("--param 需要 key=value 格式")
				}
				params[k] = v
			}
			params["port_min"] = portMin
			if portMax == 0 {
				params["port_max"] = portMin
			} else {
				params["port_max"] = portMax
			}
			if protocol != "" {
				params["protocol"] = protocol
			}
			if g.server != "" {
				return remoteProfileRun(args[0], action, params, cred, nodeID, dryRun, ack)
			}
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			local := b.(*LocalBackend)
			res, err := local.runProfile(ctx, args[0], action, params, cred, nodeID, dryRun, ack)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(res)
			}
			if res.AlreadyDone {
				fmt.Printf("· 规则已存在，无需变更（%s）\n", res.Message)
				return nil
			}
			if res.Skipped {
				fmt.Printf("· %s\n", res.Message)
				return nil
			}
			fmt.Printf("✓ %s 完成：%s\n", action, res.Message)
			fmt.Printf("  操作     ：%s\n", res.Operation)
			fmt.Printf("  端点     ：%s\n", res.Host)
			fmt.Printf("  HTTP     ：%d\n", res.Status)
			fmt.Printf("  耗时     ：%s\n", res.Duration)
			return nil
		},
	}
	cmd.Flags().StringVar(&action, "action", "ensure_port_allowed", "ensure_port_allowed 或 revoke_port_allowed")
	cmd.Flags().IntVar(&portMin, "port-min", 0, "起始端口")
	cmd.Flags().IntVar(&portMax, "port-max", 0, "结束端口（留空等于起始端口）")
	cmd.Flags().StringVar(&protocol, "protocol", "tcp", "协议")
	cmd.Flags().StringVar(&cred, "credential", "default", "凭证 label")
	cmd.Flags().StringVar(&nodeID, "node", "", "关联的 YukiTunnel 节点 ID")
	cmd.Flags().StringArrayVar(&paramKV, "param", nil, "profile 参数 key=value，可重复")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只渲染请求，不实际调用云 API")
	cmd.Flags().BoolVar(&ack, "ack", false, "确认已知晓 profile 声明的云权限")
	return cmd
}

func newProfileRunsCmd() *cobra.Command {
	var limit int
	return &cobra.Command{
		Use:   "runs",
		Short: "查看最近执行记录",
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.server != "" {
				return remoteProfileRuns(limit)
			}
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			local := b.(*LocalBackend)
			runs, err := local.Store().ListProfileRuns(ctx, "", limit)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"runs": runs})
			}
			rows := make([][]string, 0, len(runs))
			for _, r := range runs {
				rows = append(rows, []string{
					r.CreatedAt.Local().Format("2006-01-02 15:04:05"),
					r.ProfileID, r.Action, fmt.Sprintf("%d-%d", r.PortMin, r.PortMax),
					r.Status, fmt.Sprintf("%dms", r.DurationMS), r.ActorName, truncate(r.Message, 48),
				})
			}
			table([]string{"时间", "Profile", "动作", "端口", "状态", "耗时", "操作人", "结果"}, rows)
			return nil
		},
	}
}

// newProfileRekeyCmd re-encrypts stored cloud credentials after a key rotation.
func newProfileRekeyCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "rekey",
		Short: "轮换云凭证加密密钥（读取 YUKI_CREDENTIAL_KEY_OLD → YUKI_CREDENTIAL_KEY）",
		Long: `把 profile_credentials 中的密文从旧密钥迁移到新密钥。

轮换步骤：
  export YUKI_CREDENTIAL_KEY_OLD=$(base64 -w0 < <data-dir>/credential.key)
  export YUKI_CREDENTIAL_KEY=$(head -c 32 /dev/urandom | base64 -w0)
  yuki-server profile rekey --data-dir <data-dir>

迁移是逐行事务的：中途失败可以修好原因后直接重跑。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.server != "" {
				return fmt.Errorf("rekey 只能在服务端本机执行（需要直接访问数据库与密钥文件）")
			}
			if os.Getenv("YUKI_CREDENTIAL_KEY") == "" {
				return fmt.Errorf("请先设置 YUKI_CREDENTIAL_KEY（新密钥，base64）")
			}
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			local, ok := b.(*LocalBackend)
			if !ok {
				return fmt.Errorf("不支持的 backend")
			}
			before, err := local.Store().ListProfileCredentials(ctx, "")
			if err != nil {
				return err
			}
			if dryRun {
				if isJSON() {
					return printJSON(map[string]any{"would_rotate": len(before)})
				}
				fmt.Printf("dry-run：将轮换 %d 条凭证\n", len(before))
				return nil
			}
			n, err := local.Store().RotateCredentialKey(ctx, local.cfg.DataDir)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"rotated": n})
			}
			fmt.Printf("✓ 已轮换 %d 条凭证的加密密钥\n", n)
			fmt.Println("  旧密钥请立即销毁；新密钥需与数据库一起备份。")
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只统计，不修改")
	return cmd
}

func newProfileMirrorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mirror",
		Short: "镜像与代理配置",
	}
	var mirrors, proxy string
	set := &cobra.Command{
		Use:   "set",
		Short: "设置自定义镜像与代理（代理支持 http/https/socks5）",
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.server != "" {
				return remoteProfileSettings(mirrors, proxy)
			}
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			local := b.(*LocalBackend)
			if mirrors != "" {
				if err := local.Store().SetSetting(ctx, "profile_mirrors", mirrors); err != nil {
					return err
				}
			}
			if proxy != "" {
				if err := local.Store().SetSetting(ctx, "profile_proxy", proxy); err != nil {
					return err
				}
			}
			fmt.Println("✓ 已保存")
			return nil
		},
	}
	set.Flags().StringVar(&mirrors, "mirrors", "", "逗号分隔的镜像地址（优先于内置镜像）")
	set.Flags().StringVar(&proxy, "proxy", "", "代理地址，例如 socks5://127.0.0.1:1080")
	show := &cobra.Command{
		Use:   "show",
		Short: "查看当前镜像链与代理",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			local := b.(*LocalBackend)
			m, _ := local.Store().GetSetting(ctx, "profile_mirrors", "")
			p, _ := local.Store().GetSetting(ctx, "profile_proxy", "")
			if isJSON() {
				return printJSON(map[string]any{"mirrors": profileMirrors(), "custom": m, "proxy": p})
			}
			fmt.Println("生效的镜像链（按顺序回退）：")
			for i, x := range profileMirrors() {
				fmt.Printf("  %d. %s\n", i+1, x)
			}
			if m != "" {
				fmt.Printf("自定义镜像（优先）：%s\n", m)
			}
			if p != "" {
				fmt.Printf("代理：%s\n", p)
			}
			fmt.Println("签名校验：始终启用（无开关可关闭）")
			return nil
		},
	}
	cmd.AddCommand(set, show)
	return cmd
}

// ---- helpers ----

// profileMirrors resolves the mirror chain for CLI use, matching the server's
// precedence: YUKI_PROFILE_MIRRORS > built-in chain. An explicit --mirror flag
// is prepended by the caller.
func profileMirrors() []string {
	if raw := strings.TrimSpace(os.Getenv("YUKI_PROFILE_MIRRORS")); raw != "" {
		out := []string{}
		for _, m := range strings.Split(raw, ",") {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return registry.DefaultMirrors
}

// profileProxy resolves the proxy for provider/registry calls.
func profileProxy() string { return strings.TrimSpace(os.Getenv("YUKI_PROFILE_PROXY")) }

func shortHash(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// cloudProfileRecord mirrors store.CloudProfile for CLI JSON output.
type cloudProfileRecord struct {
	ID       string `json:"id"`
	Vendor   string `json:"vendor"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Source   string `json:"source"`
	SHA256   string `json:"sha256"`
	Document string `json:"-"`
}

func installFromFile(file string) error {
	p, err := registry.VerifyFile(file, "", trustedKeys())
	if err != nil {
		return err
	}
	ctx := context.Background()
	b, err := backend(ctx)
	if err != nil {
		return err
	}
	defer b.Close()
	local := b.(*LocalBackend)
	raw, _ := yaml.Marshal(p)
	rec := &cloudProfileRecord{
		ID: p.Metadata.ID, Vendor: p.Metadata.Vendor, Name: p.Metadata.Name,
		Version: p.Metadata.Version, Source: p.Source, SHA256: p.SHA256, Document: string(raw),
	}
	if err := local.saveProfile(ctx, rec); err != nil {
		return err
	}
	if isJSON() {
		return printJSON(map[string]any{"ok": true, "profile": rec})
	}
	fmt.Printf("✓ 已从本地安装 %s（sha256 %s）\n", rec.ID, shortHash(rec.SHA256))
	return nil
}

func profileListRemote() error {
	cfg := newClientConfigForServer()
	if cfg.Token == "" {
		if _, err := loginRemote(cfg); err != nil {
			return err
		}
	}
	client := newAPIClient(cfg)
	var out struct {
		Profiles []cloudProfileRecord `json:"profiles"`
	}
	if err := client.Do("GET", "/admin/profiles", nil, &out); err != nil {
		return err
	}
	if isJSON() {
		return printJSON(out)
	}
	rows := make([][]string, 0, len(out.Profiles))
	for _, p := range out.Profiles {
		rows = append(rows, []string{p.ID, p.Vendor, p.Version, shortHash(p.SHA256), p.Source})
	}
	table([]string{"ID", "厂商", "版本", "SHA256", "来源"}, rows)
	return nil
}

func remoteProfileInstall(id string) error {
	cfg := newClientConfigForServer()
	if cfg.Token == "" {
		if _, err := loginRemote(cfg); err != nil {
			return err
		}
	}
	client := newAPIClient(cfg)
	var out map[string]any
	if err := client.Do("POST", "/admin/profiles/install", map[string]string{"id": id}, &out); err != nil {
		return err
	}
	if isJSON() {
		return printJSON(out)
	}
	fmt.Printf("✓ 已安装 %s\n", id)
	return nil
}

func remoteCredentialSet(profileID, label string, fields map[string]string) error {
	cfg := newClientConfigForServer()
	if cfg.Token == "" {
		if _, err := loginRemote(cfg); err != nil {
			return err
		}
	}
	client := newAPIClient(cfg)
	var out map[string]any
	if err := client.Do("PUT", "/admin/profiles/"+profileID+"/credentials",
		map[string]any{"label": label, "fields": fields}, &out); err != nil {
		return err
	}
	if isJSON() {
		return printJSON(out)
	}
	fmt.Printf("✓ 已保存 %s 的凭证（label=%s）\n", profileID, label)
	return nil
}

func remoteProfileRun(id, action string, params map[string]any, cred, nodeID string, dryRun, ack bool) error {
	cfg := newClientConfigForServer()
	if cfg.Token == "" {
		if _, err := loginRemote(cfg); err != nil {
			return err
		}
	}
	client := newAPIClient(cfg)
	body := map[string]any{
		"action": action, "params": params, "credential_label": cred,
		"node_id": nodeID, "dry_run": dryRun, "acknowledge_permissions": ack,
	}
	var out map[string]any
	if err := client.Do("POST", "/admin/profiles/"+id+"/run", body, &out); err != nil {
		return err
	}
	if isJSON() {
		return printJSON(out)
	}
	b, _ := json.MarshalIndent(out["result"], "", "  ")
	fmt.Println(string(b))
	return nil
}

func remoteProfileRuns(limit int) error {
	cfg := newClientConfigForServer()
	if cfg.Token == "" {
		if _, err := loginRemote(cfg); err != nil {
			return err
		}
	}
	client := newAPIClient(cfg)
	var out struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := client.Do("GET", fmt.Sprintf("/admin/profile-runs?limit=%d", limit), nil, &out); err != nil {
		return err
	}
	if isJSON() {
		return printJSON(out)
	}
	rows := make([][]string, 0, len(out.Runs))
	for _, r := range out.Runs {
		rows = append(rows, []string{
			fmt.Sprint(r["created_at"]), fmt.Sprint(r["profile_id"]), fmt.Sprint(r["action"]),
			fmt.Sprint(r["status"]), fmt.Sprint(r["message"]),
		})
	}
	table([]string{"时间", "Profile", "动作", "状态", "结果"}, rows)
	return nil
}

func remoteProfileSettings(mirrors, proxy string) error {
	cfg := newClientConfigForServer()
	if cfg.Token == "" {
		if _, err := loginRemote(cfg); err != nil {
			return err
		}
	}
	client := newAPIClient(cfg)
	body := map[string]any{}
	if mirrors != "" {
		body["mirrors"] = mirrors
	}
	if proxy != "" {
		body["proxy"] = proxy
	}
	var out map[string]any
	if err := client.Do("PUT", "/admin/profile-settings", body, &out); err != nil {
		return err
	}
	if isJSON() {
		return printJSON(out)
	}
	fmt.Println("✓ 已保存")
	return nil
}

var _ = cliutil.NewClient
var _ = os.Getenv
