package cli

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "查看和修改本地客户端配置",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "显示当前配置（隐藏令牌）",
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := cliutil.LoadClientConfig()
				if err != nil {
					return err
				}
				if flags.server != "" {
					cfg.ServerURL = cliutil.NormalizeServerURL(flags.server)
				}
				if flags.jsonOut {
					return printer().JSONOut(map[string]any{
						"path": cliutil.ClientConfigPath(), "server_url": cfg.ServerURL,
						"username": cfg.Username, "logged_in": cfg.Token != "",
						"key_path": cfg.KeyPath, "insecure_skip_verify": cfg.InsecureSkipVerify,
						"auto_reconnect": cfg.AutoReconnect,
					})
				}
				fmt.Printf("配置文件：%s\n", cliutil.ClientConfigPath())
				fmt.Printf("服务端  ：%s\n", cfg.ServerURL)
				fmt.Printf("用户名  ：%s\n", cfg.Username)
				fmt.Printf("登录状态：%s\n", map[bool]string{true: "已登录", false: "未登录"}[cfg.Token != ""])
				if cfg.KeyPath != "" {
					fmt.Printf("RSA 私钥：%s\n", cfg.KeyPath)
				}
				fmt.Printf("自签名信任：%s\n", cliutil.YesNo(cfg.InsecureSkipVerify))
				fmt.Printf("自动重连：%s\n", cliutil.YesNo(cfg.AutoReconnect))
				return nil
			},
		},
		&cobra.Command{
			Use:   "set <键> <值>",
			Short: "修改配置项（server / auto-reconnect / insecure）",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := cliutil.LoadClientConfig()
				if err != nil {
					return err
				}
				switch args[0] {
				case "server":
					cfg.ServerURL = cliutil.NormalizeServerURL(args[1])
				case "auto-reconnect":
					cfg.AutoReconnect = args[1] == "true" || args[1] == "1" || args[1] == "on"
				case "insecure":
					cfg.InsecureSkipVerify = args[1] == "true" || args[1] == "1" || args[1] == "on"
				default:
					return errors.New("未知配置项，可用：server / auto-reconnect / insecure")
				}
				if err := cfg.Save(); err != nil {
					return err
				}
				if flags.jsonOut {
					return printer().JSONOut(map[string]any{"ok": true, "key": args[0], "value": args[1]})
				}
				fmt.Printf("✓ 已更新 %s\n", args[0])
				return nil
			},
		},
		&cobra.Command{
			Use:   "path",
			Short: "显示配置文件路径",
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Println(cliutil.ClientConfigPath())
				return nil
			},
		},
	)
	return cmd
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "诊断本地环境与服务端连通性",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliutil.LoadClientConfig()
			if err != nil {
				return err
			}
			if flags.server != "" {
				cfg.ServerURL = cliutil.NormalizeServerURL(flags.server)
			}
			results := []map[string]any{}
			add := func(name string, ok bool, detail string) {
				results = append(results, map[string]any{"check": name, "ok": ok, "detail": detail})
			}
			add("配置文件", true, cliutil.ClientConfigPath())
			add("服务端地址", cfg.ServerURL != "", cfg.ServerURL)
			add("登录凭证", cfg.Token != "", map[bool]string{true: "已保存令牌", false: "未登录"}[cfg.Token != ""])
			if cfg.ServerURL != "" {
				c := cliutil.NewClient(cfg)
				var health map[string]any
				if err := c.Do("GET", "/health", nil, &health); err != nil {
					add("服务端健康检查", false, cliutil.RenderError(err))
				} else {
					add("服务端健康检查", true, fmt.Sprintf("%v", health["version"]))
				}
			}
			if flags.jsonOut {
				return printer().JSONOut(map[string]any{"checks": results})
			}
			for _, r := range results {
				icon := "✓"
				if !r["ok"].(bool) {
					icon = "✗"
				}
				fmt.Printf("%s %-16s %v\n", icon, r["check"], r["detail"])
			}
			return nil
		},
	}
}
