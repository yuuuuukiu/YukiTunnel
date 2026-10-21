package servercli

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/auth"
	"github.com/yuuuuukiu/yukitunnel/internal/config"
	"github.com/yuuuuukiu/yukitunnel/internal/ids"
	"github.com/yuuuuukiu/yukitunnel/internal/store"
	"github.com/yuuuuukiu/yukitunnel/internal/tlsutil"
	"github.com/yuuuuukiu/yukitunnel/internal/version"
)

func versionString() string { return version.Version }

func newInitCmd() *cobra.Command {
	var (
		apiAddr      string
		controlAddr  string
		publicHost   string
		adminUser    string
		adminPass    string
		domainSuffix string
		binding      bool
		portMin      int
		portMax      int
		certFile     string
		keyFile      string
		force        bool
		noAdminToken bool
		tokenTTL     int
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "初始化服务端：数据目录、TLS、管理员账号和默认节点",
		Long: `初始化服务端。该命令可重复执行：已存在的配置不会被覆盖（除非 --force）。

执行完成后会生成：
  <data-dir>/config.yaml      服务端配置
  <data-dir>/yukitunnel.db    SQLite 数据库
  <data-dir>/tls/server.crt   自签名证书（未指定 --tls-cert 时）
  <data-dir>/admin.txt        首次生成的管理员密码（仅当未指定 --admin-password）
  <data-dir>/admin-token.txt  管理员 API 令牌（用于远程管理）

之后可直接运行 yuki-server start 启动服务端。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := dataDir()
			if err := ensureDir(dir); err != nil {
				return fmt.Errorf("创建数据目录失败：%w", err)
			}
			cfgPath := configPath()
			cfg, err := config.LoadOrEmpty(cfgPath)
			if err != nil {
				return err
			}
			if _, err := os.Stat(cfgPath); err == nil && !force {
				return fmt.Errorf("配置已存在：%s（如需重新初始化请加 --force）", cfgPath)
			}
			cfg.DataDir = dir
			if apiAddr != "" {
				cfg.APIAddr = apiAddr
			}
			if controlAddr != "" {
				cfg.ControlAddr = controlAddr
			}
			if publicHost != "" {
				cfg.PublicHost = publicHost
			}
			if domainSuffix != "" {
				cfg.Domain.Suffix = domainSuffix
			}
			if cmd.Flags().Changed("domain-binding") {
				cfg.Domain.Binding = binding
			}
			if portMin > 0 {
				cfg.Ports.Min = portMin
			}
			if portMax > 0 {
				cfg.Ports.Max = portMax
			}
			if certFile != "" && keyFile != "" {
				cfg.TLS.CertFile = certFile
				cfg.TLS.KeyFile = keyFile
			}
			cfg.Normalize()
			if err := cfg.Validate(); err != nil {
				return err
			}
			if err := checkPortFree(cfg.APIAddr); err != nil {
				return err
			}
			if err := checkPortFree(cfg.ControlAddr); err != nil {
				return err
			}
			if cfg.TLS.CertFile == "" {
				hosts := []string{"localhost", cfg.PublicHost}
				if ip := detectIP(); ip != "" {
					hosts = append(hosts, ip)
				}
				if cfg.Domain.Suffix != "" {
					hosts = append(hosts, "*."+cfg.Domain.Suffix)
				}
				files, err := tlsutil.EnsureSelfSigned(dir, hosts)
				if err != nil {
					return fmt.Errorf("生成 TLS 证书失败：%w", err)
				}
				cfg.TLS.CertFile = files.CertFile
				cfg.TLS.KeyFile = files.KeyFile
			}
			if err := cfg.Save(cfgPath); err != nil {
				return fmt.Errorf("写入配置失败：%w", err)
			}

			// Database + admin bootstrap
			st, err := store.Open(cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			createdAdmin := false
			generatedPass := ""
			admins, err := st.CountAdmins(ctx)
			if err != nil {
				return err
			}
			if admins == 0 {
				user := adminUser
				if user == "" {
					user = "admin"
				}
				pass := adminPass
				if pass == "" {
					pass = ids.Token()[:16]
					generatedPass = pass
				}
				hash, err := auth.HashPassword(pass)
				if err != nil {
					return err
				}
				u := &store.User{ID: ids.Hex(12), Username: user, DisplayName: "Administrator", PasswordHash: hash, Role: "admin"}
				if err := st.CreateUser(ctx, u); err != nil {
					return fmt.Errorf("创建管理员失败：%w", err)
				}
				createdAdmin = true
				if generatedPass != "" {
					adminFile := filepath.Join(dir, "admin.txt")
					_ = os.WriteFile(adminFile, []byte(fmt.Sprintf("username: %s\npassword: %s\ncreated: %s\n请登录后立即修改密码并删除本文件。\n",
						user, generatedPass, time.Now().Format(time.RFC3339))), 0o600)
				}
			}
			nodes, _ := st.ListNodes(ctx)
			if len(nodes) == 0 {
				_, portStr, _ := net.SplitHostPort(cfg.ControlAddr)
				port, _ := strconv.Atoi(portStr)
				host := cfg.PublicHost
				if host == "" {
					host = detectIP()
				}
				n := &store.Node{
					ID: ids.Hex(8), Name: "default", Region: "local", PublicHost: host,
					ControlHost: "127.0.0.1", ControlPort: port, Enabled: true, Status: "online",
				}
				if err := st.CreateNode(ctx, n); err != nil {
					return err
				}
			}
			defaults := map[string]string{
				"domain_binding": strconv.FormatBool(cfg.Domain.Binding),
				"domain_suffix":  cfg.Domain.Suffix,
				"public_host":    cfg.PublicHost,
				"port_min":       strconv.Itoa(cfg.Ports.Min),
				"port_max":       strconv.Itoa(cfg.Ports.Max),
			}
			for k, v := range defaults {
				if cur, _ := st.GetSetting(ctx, k, ""); cur == "" {
					_ = st.SetSetting(ctx, k, v)
				}
			}
			var adminTokenVal string
			tokenFile := filepath.Join(dir, "admin-token.txt")
			if !noAdminToken {
				if !force {
					if b, err := os.ReadFile(tokenFile); err == nil {
						for _, line := range strings.Split(string(b), "\n") {
							line = strings.TrimSpace(line)
							if strings.HasPrefix(line, "token:") {
								adminTokenVal = strings.TrimSpace(strings.TrimPrefix(line, "token:"))
								break
							}
						}
					}
				}
				if adminTokenVal == "" {
					plain, terr := createAdminToken(ctx, st, tokenTTL)
					if terr != nil {
						return fmt.Errorf("创建管理员令牌失败：%w", terr)
					}
					adminTokenVal = plain
					_ = os.WriteFile(tokenFile, []byte(fmt.Sprintf(
						"# YukiTunnel 管理员 API 令牌（用于远程管理）\n"+
							"# 远程管理命令：yuki-server --server https://<服务端IP>:8443 --token <此值> <命令>\n"+
							"token: %s\nexpires: %s\n",
						plain, time.Now().Add(time.Duration(tokenTTL)*time.Hour).Format(time.RFC3339))), 0o600)
				}
			}

			fp, _ := tlsutil.FingerprintFile(cfg.TLS.CertFile)
			out := map[string]any{
				"ok": true, "data_dir": dir, "config": cfgPath,
				"api_addr": cfg.APIAddr, "control_addr": cfg.ControlAddr,
				"public_host": cfg.PublicHost, "domain_binding": cfg.Domain.Binding,
				"domain_suffix": cfg.Domain.Suffix, "port_range": fmt.Sprintf("%d-%d", cfg.Ports.Min, cfg.Ports.Max),
				"admin_created": createdAdmin, "admin_user": adminUser,
				"tls_cert": cfg.TLS.CertFile, "tls_fingerprint": fp,
			}
			if adminTokenVal != "" {
				out["admin_token"] = adminTokenVal
				out["admin_token_file"] = tokenFile
			}
			if isJSON() {
				return printJSON(out)
			}
			fmt.Println("✓ 服务端初始化完成")
			fmt.Printf("  数据目录   ：%s\n", dir)
			fmt.Printf("  配置文件   ：%s\n", cfgPath)
			fmt.Printf("  API 地址   ：https://%s\n", cfg.APIAddr)
			fmt.Printf("  控制端口   ：%s\n", cfg.ControlAddr)
			fmt.Printf("  公网入口   ：%s\n", cfg.PublicHost)
			fmt.Printf("  端口范围   ：%d-%d\n", cfg.Ports.Min, cfg.Ports.Max)
			fmt.Printf("  TLS 指纹   ：%s\n", fp)
			if createdAdmin {
				if generatedPass != "" {
					fmt.Printf("\n  管理员账号 ：%s\n  初始密码   ：%s（已写入 %s，请登录后立即修改）\n",
						adminUser, generatedPass, filepath.Join(dir, "admin.txt"))
				} else {
					fmt.Printf("\n  管理员账号 ：%s（密码为你指定的值）\n", adminUser)
				}
			}

			if adminTokenVal != "" {
				apiHost := cfg.APIAddr
				if strings.HasPrefix(apiHost, "0.0.0.0:") {
					pub := cfg.PublicHost
					if pub == "" {
						pub = "<服务端IP>"
					}
					_, port, _ := net.SplitHostPort(apiHost)
					apiHost = net.JoinHostPort(pub, port)
				}
				fmt.Println("\n============================================================")
				fmt.Println("  管理员远程管理令牌 (Admin API Token)")
				fmt.Println("============================================================")
				fmt.Printf("  管理令牌 (Token) ：%s\n", adminTokenVal)
				fmt.Printf("  令牌凭据文件     ：%s (0600 权限)\n", tokenFile)
				fmt.Printf("  远程管理命令示例 ：\n")
				fmt.Printf("    yuki-server --server https://%s --token %s status\n", apiHost, adminTokenVal)
				fmt.Printf("    yuki-server --server https://%s --token %s user list\n", apiHost, adminTokenVal)
				fmt.Printf("    yuki-server --server https://%s --token %s tunnel list\n", apiHost, adminTokenVal)
				fmt.Println("============================================================")
			}

			fmt.Printf("\n下一步：yuki-server start\n")
			return nil
		},
	}
	cmd.Flags().StringVar(&apiAddr, "api-addr", "0.0.0.0:8443", "HTTPS API/WebUI 监听地址")
	cmd.Flags().StringVar(&controlAddr, "control-addr", "0.0.0.0:9443", "隧道控制监听地址")
	cmd.Flags().StringVar(&publicHost, "public-host", "", "对外公网 IP 或域名（留空自动探测）")
	cmd.Flags().StringVar(&adminUser, "admin-user", "admin", "管理员用户名")
	cmd.Flags().StringVar(&adminPass, "admin-password", "", "管理员密码（留空则随机生成并写入 admin.txt）")
	cmd.Flags().StringVar(&domainSuffix, "domain-suffix", "", "域名绑定后缀，例如 tunnel.example.com")
	cmd.Flags().BoolVar(&binding, "domain-binding", false, "启用域名绑定（隧道返回域名+端口）")
	cmd.Flags().IntVar(&portMin, "port-min", 20000, "公网端口范围下限")
	cmd.Flags().IntVar(&portMax, "port-max", 30000, "公网端口范围上限")
	cmd.Flags().StringVar(&certFile, "tls-cert", "", "已有 TLS 证书路径")
	cmd.Flags().StringVar(&keyFile, "tls-key", "", "已有 TLS 私钥路径")
	cmd.Flags().BoolVar(&force, "force", false, "覆盖已有配置")
	cmd.Flags().BoolVar(&noAdminToken, "no-admin-token", false, "不生成远程管理 API 令牌")
	cmd.Flags().IntVar(&tokenTTL, "admin-token-ttl-hours", defaultTokenTTL(), "管理员 API 令牌有效期（小时，默认 8760 即 1 年）")
	return cmd
}

func checkPortFree(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("端口不可用 %s：%w（请关闭占用程序或改用其他端口）", addr, err)
	}
	_ = l.Close()
	return nil
}

func detectIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

var _ = strings.TrimSpace

// createAdminToken mints an API token for the first admin account.
//
// The plaintext is returned once and never stored: the database keeps only a
// SHA-256 hash, so a lost token must be recreated rather than recovered.
func createAdminToken(ctx context.Context, st *store.Store, ttlHours int) (string, error) {
	admins, err := st.ListUsers(ctx)
	if err != nil {
		return "", err
	}
	var admin *store.User
	for i := range admins {
		if admins[i].Role == "admin" {
			admin = &admins[i]
			break
		}
	}
	if admin == nil {
		return "", fmt.Errorf("没有可用的管理员账号")
	}
	if ttlHours <= 0 {
		ttlHours = defaultTokenTTL()
	}
	plain, hash := auth.NewToken()
	now := time.Now().UTC()
	tok := &store.APIToken{
		ID:        ids.Hex(12),
		UserID:    admin.ID,
		Name:      "admin-cli",
		TokenHash: hash,
		Scope:     "admin",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(ttlHours) * time.Hour),
	}
	if err := st.CreateAPIToken(ctx, tok); err != nil {
		return "", err
	}
	return plain, nil
}

func defaultTokenTTL() int {
	if s := os.Getenv("YUKI_ADMIN_TOKEN_TTL_HOURS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return 8760 // 1 year default
}
