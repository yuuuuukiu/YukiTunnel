package servercli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "域名绑定开关",
	}
	var suffix string
	binding := &cobra.Command{
		Use:   "binding",
		Short: "开启或关闭域名绑定（yuki-server domain binding on|off）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch strings.ToLower(args[0]) {
			case "on", "enable", "true", "1":
				return setBinding(true, suffix)
			case "off", "disable", "false", "0":
				return setBinding(false, "")
			default:
				return fmt.Errorf("参数必须是 on 或 off")
			}
		},
	}
	binding.Flags().StringVar(&suffix, "suffix", "", "域名后缀，例如 tunnel.example.com")
	show := &cobra.Command{
		Use:   "show",
		Short: "查看域名绑定设置",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			s, err := b.Settings(ctx)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"domain_binding": s.DomainBinding, "domain_suffix": s.DomainSuffix})
			}
			fmt.Printf("域名绑定：%s\n", map[bool]string{true: "已开启", false: "已关闭"}[s.DomainBinding])
			if s.DomainSuffix != "" {
				fmt.Printf("域名后缀：%s\n", s.DomainSuffix)
			}
			return nil
		},
	}
	cmd.AddCommand(binding, show)
	return cmd
}

func setBinding(on bool, suffix string) error {
	ctx := context.Background()
	b, err := backend(ctx)
	if err != nil {
		return err
	}
	defer b.Close()
	patch := SettingsPatch{DomainBinding: &on}
	if suffix != "" {
		patch.DomainSuffix = &suffix
	}
	if on {
		cur, err := b.Settings(ctx)
		if err != nil {
			return err
		}
		s := suffix
		if s == "" {
			s = cur.DomainSuffix
		}
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("开启域名绑定需要 --suffix，例如 --suffix tunnel.example.com")
		}
		patch.DomainSuffix = &s
	}
	s, err := b.UpdateSettings(ctx, patch)
	if err != nil {
		return err
	}
	if isJSON() {
		return printJSON(map[string]any{"ok": true, "settings": s})
	}
	if on {
		fmt.Printf("✓ 已开启域名绑定（后缀 %s）\n", s.DomainSuffix)
		fmt.Println("  新建隧道将返回「域名+端口」地址；已有隧道保持原地址，可编辑后重新分配。")
	} else {
		fmt.Println("✓ 已关闭域名绑定，新建隧道将返回「公网 IP+端口」地址。")
	}
	return nil
}

func newPortRangeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "port-range",
		Short: "公网端口范围配置",
	}
	var min, max int
	set := &cobra.Command{
		Use:   "set",
		Short: "设置可分配的端口范围",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			s, err := b.UpdateSettings(ctx, SettingsPatch{PortMin: &min, PortMax: &max})
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "settings": s})
			}
			fmt.Printf("✓ 端口范围已设置为 %d-%d\n", s.PortMin, s.PortMax)
			return nil
		},
	}
	set.Flags().IntVar(&min, "min", 20000, "起始端口")
	set.Flags().IntVar(&max, "max", 30000, "结束端口")
	list := &cobra.Command{
		Use:   "list",
		Short: "查看端口范围与已占用端口",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			usage, err := b.PortUsage(ctx)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(usage)
			}
			fmt.Printf("端口范围：%d-%d\n", usage.Range.Min, usage.Range.Max)
			for _, n := range usage.Nodes {
				fmt.Printf("  节点 %s：已占用 %d 个端口 %v\n", n.Name, len(n.Used), n.Used)
			}
			return nil
		},
	}
	cmd.AddCommand(set, list)
	return cmd
}

func newAuditCmd() *cobra.Command {
	var limit, offset int
	var action string
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "查看审计日志",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "列出最近的审计记录",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			entries, err := b.ListAudit(ctx, limit, offset)
			if err != nil {
				return err
			}
			filtered := entries
			if action != "" {
				filtered = filtered[:0]
				for _, e := range entries {
					if strings.HasPrefix(e.Action, action) {
						filtered = append(filtered, e)
					}
				}
			}
			if isJSON() {
				return printJSON(map[string]any{"entries": filtered})
			}
			rows := make([][]string, 0, len(filtered))
			for _, e := range filtered {
				rows = append(rows, []string{
					e.CreatedAt.Local().Format("2006-01-02 15:04:05"),
					e.ActorName, e.Action, e.TargetType + ":" + e.TargetID, e.Detail, e.IP,
				})
			}
			table([]string{"时间", "操作人", "动作", "对象", "详情", "来源 IP"}, rows)
			return nil
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "返回条数")
	list.Flags().IntVar(&offset, "offset", 0, "偏移量")
	list.Flags().StringVar(&action, "action", "", "按动作前缀过滤，例如 tunnel.")
	cmd.AddCommand(list)
	return cmd
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "服务端配置查看与修改",
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "显示当前配置",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadServerConfig()
			if err != nil {
				return err
			}
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			s, err := b.Settings(ctx)
			if err != nil {
				return err
			}
			out := map[string]any{
				"data_dir": cfg.DataDir, "config_file": configPath(),
				"api_addr": cfg.APIAddr, "control_addr": cfg.ControlAddr,
				"tls_cert": cfg.TLS.CertFile, "database": cfg.DatabaseURL,
				"domain_binding": s.DomainBinding, "domain_suffix": s.DomainSuffix,
				"public_host": s.PublicHost, "port_min": s.PortMin, "port_max": s.PortMax,
				"mode": b.Mode(),
			}
			if isJSON() {
				return printJSON(out)
			}
			for _, k := range []string{"data_dir", "config_file", "api_addr", "control_addr", "tls_cert", "database", "public_host", "mode"} {
				fmt.Printf("%-14s %v\n", k+":", out[k])
			}
			fmt.Printf("%-14s %v\n", "domain_binding:", map[bool]string{true: "on", false: "off"}[s.DomainBinding])
			fmt.Printf("%-14s %s\n", "domain_suffix:", s.DomainSuffix)
			fmt.Printf("%-14s %d-%d\n", "port_range:", s.PortMin, s.PortMax)
			return nil
		},
	}
	set := &cobra.Command{
		Use:   "set <键> <值>",
		Short: "修改配置项（domain-suffix / public-host / port-min / port-max）",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			var patch SettingsPatch
			switch args[0] {
			case "domain-suffix":
				patch.DomainSuffix = &args[1]
			case "public-host":
				patch.PublicHost = &args[1]
			case "port-min", "port-max":
				n, err := strconv.Atoi(args[1])
				if err != nil {
					return fmt.Errorf("%s 必须是数字", args[0])
				}
				if args[0] == "port-min" {
					patch.PortMin = &n
				} else {
					patch.PortMax = &n
				}
			default:
				return fmt.Errorf("未知配置项 %q", args[0])
			}
			s, err := b.UpdateSettings(ctx, patch)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "settings": s})
			}
			fmt.Printf("✓ 已更新 %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(show, set)
	return cmd
}

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "管理员 API 令牌（远程模式使用）",
	}
	var ttl int
	var name string
	create := &cobra.Command{
		Use:   "create",
		Short: "为当前管理员创建 API 令牌（远程模式）",
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.server == "" {
				return fmt.Errorf("请使用 --server 指定服务端地址，并用 --username/--password 登录")
			}
			cfg := newClientConfigForServer()
			if cfg.Token == "" {
				if _, err := loginRemote(cfg); err != nil {
					return err
				}
			}
			client := newAPIClient(cfg)
			var out struct {
				Token string `json:"token"`
				Meta  any    `json:"meta"`
			}
			if err := client.Do("POST", "/me/tokens", map[string]any{"name": name, "ttl_hours": ttl}, &out); err != nil {
				return err
			}
			if isJSON() {
				return printJSON(out)
			}
			fmt.Println("✓ 已创建 API 令牌（仅显示一次）：")
			fmt.Println(out.Token)
			return nil
		},
	}
	create.Flags().IntVar(&ttl, "ttl-hours", 720, "有效期（小时）")
	create.Flags().StringVar(&name, "name", "cli", "令牌名称")
	cmd.AddCommand(create)
	return cmd
}
