package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
)

func newLoginCmd() *cobra.Command {
	var (
		password string
		useKey   bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "登录服务端（密码或 RSA Key）",
		Example: `  # 密码登录（交互输入密码）
  yuki login --server https://panel.example.com:8443 --username alice

  # RSA Key 登录
  yuki login --server https://panel.example.com:8443 --username alice --key ~/.ssh/id_rsa`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliutil.LoadClientConfig()
			if err != nil {
				return err
			}
			if flags.server != "" {
				cfg.ServerURL = cliutil.NormalizeServerURL(flags.server)
			}
			if cfg.ServerURL == "" {
				cfg.ServerURL = cliutil.NormalizeServerURL(prompt("服务端地址（例如 https://1.2.3.4:8443）: "))
			}
			user := flags.user
			if user == "" {
				user = strings.TrimSpace(prompt("用户名: "))
			}
			if user == "" {
				return errors.New("用户名不能为空")
			}
			if flags.insecure {
				cfg.InsecureSkipVerify = true
			}
			keyPath := flags.keyPath
			if useKey && keyPath == "" {
				keyPath = strings.TrimSpace(prompt("RSA 私钥路径: "))
			}
			var res *cliutil.LoginResult
			if keyPath != "" {
				res, err = cliutil.LoginWithKey(cfg, user, expandHome(keyPath))
			} else {
				if password == "" {
					password, err = readPassword("密码: ")
					if err != nil {
						return err
					}
				}
				res, err = cliutil.Login(cfg, user, password)
			}
			if err != nil {
				return err
			}
			cfg.KeyPath = keyPath
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("保存登录状态失败：%w", err)
			}
			if flags.jsonOut {
				return printer().JSONOut(map[string]any{
					"ok": true, "server": cfg.ServerURL, "user": res.User, "expires_at": res.ExpiresAt,
				})
			}
			fmt.Printf("✓ 登录成功：%s（%s）\n", res.User.Username, roleLabel(res.User.Role))
			fmt.Printf("  服务端：%s\n", cfg.ServerURL)
			fmt.Printf("  登录有效期至：%s\n", res.ExpiresAt.Local().Format("2006-01-02 15:04"))
			return nil
		},
	}
	cmd.Flags().StringVarP(&password, "password", "p", "", "密码（不推荐，建议交互输入）")
	cmd.Flags().BoolVar(&useKey, "rsa-key", false, "使用 RSA 私钥登录")
	return cmd
}

func roleLabel(role string) string {
	if role == "admin" {
		return "管理员"
	}
	return "普通用户"
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "退出登录并清除本地凭证",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliutil.LoadClientConfig()
			if err != nil {
				return err
			}
			if flags.server != "" {
				cfg.ServerURL = cliutil.NormalizeServerURL(flags.server)
			}
			if cfg.Token != "" && cfg.ServerURL != "" {
				_ = cliutil.Logout(cfg)
			}
			cfg.Token = ""
			cfg.TokenExpiry = time.Time{}
			if err := cfg.Save(); err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(map[string]any{"ok": true})
			}
			fmt.Println("✓ 已退出登录")
			return nil
		},
	}
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "显示当前登录用户",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			c := cliutil.NewClient(cfg)
			var out struct {
				User          cliutil.User `json:"user"`
				DomainBinding bool         `json:"domain_binding"`
				DomainSuffix  string       `json:"domain_suffix"`
			}
			if err := c.Do("GET", "/me", nil, &out); err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(out)
			}
			fmt.Printf("用户：%s（%s）\n", out.User.Username, roleLabel(out.User.Role))
			fmt.Printf("服务端：%s\n", cfg.ServerURL)
			fmt.Printf("域名绑定：%s\n", cliutil.YesNo(out.DomainBinding))
			if out.DomainSuffix != "" {
				fmt.Printf("域名后缀：%s\n", out.DomainSuffix)
			}
			return nil
		},
	}
}

func prompt(label string) string {
	fmt.Print(label)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	return strings.TrimSpace(s)
}

func readPassword(label string) (string, error) {
	fmt.Print(label)
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + p[1:]
		}
	}
	return p
}
