package servercli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
	"github.com/yuuuuukiu/yukitunnel/internal/config"
	"github.com/yuuuuukiu/yukitunnel/internal/server"
)

type globalFlags struct {
	jsonOut  bool
	dataDir  string
	config   string
	server   string
	token    string
	username string
	password string
	insecure bool
	yes      bool
}

var g globalFlags

// Execute runs the server CLI.
func Execute(args []string) error {
	root := newRoot()
	root.SetArgs(args)
	return root.Execute()
}

// Render formats an error for humans.
func Render(err error) string { return cliutil.RenderError(err) }

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "yuki-server",
		Short: "YukiTunnel 服务端管理工具",
		Long: `YukiTunnel 服务端管理工具（与 WebUI、桌面管理端功能对等）。

两种模式：
  本地模式（默认）：直接操作本机 SQLite 数据库，适合首次初始化与无网络环境
  远程模式：加 --server https://host:8443 --token <令牌>，管理远程服务端

示例：
  yuki-server init --data-dir /var/lib/yukitunnel --admin-user admin
  yuki-server start
  yuki-server user create alice --password '***'
  yuki-server domain binding on --suffix tunnel.example.com
  yuki-server doctor`,
		Version:       versionString(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// A release build must never ship the development signing key, so refuse
		// to run *any* subcommand when YUKI_RELEASE=1 and the key is a placeholder.
		// This makes the failure surface at `--version`, `doctor` and `serve`
		// alike, instead of only when a profile is first downloaded.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return server.CheckReleaseReadiness()
		},
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&g.jsonOut, "json", false, "以 JSON 输出")
	pf.StringVar(&g.dataDir, "data-dir", "", "数据目录（默认 /var/lib/yukitunnel 或用户配置目录）")
	pf.StringVar(&g.config, "config", "", "配置文件路径（默认 <data-dir>/config.yaml）")
	pf.StringVar(&g.server, "server", "", "远程服务端地址，例如 https://1.2.3.4:8443")
	pf.StringVar(&g.token, "token", "", "远程模式使用的管理员 API 令牌")
	pf.StringVar(&g.username, "username", "", "远程模式登录用户名")
	pf.StringVar(&g.password, "password", "", "远程模式登录密码")
	pf.BoolVar(&g.insecure, "insecure", false, "跳过 TLS 校验（自签名证书）")
	pf.BoolVarP(&g.yes, "yes", "y", false, "跳过确认提示")

	root.AddCommand(
		newInitCmd(), newStartCmd(), newStopCmd(), newRestartCmd(), newStatusCmd(),
		newDoctorCmd(), newServeCmd(),
		newUserCmd(), newNodeCmd(), newTunnelCmd(), newDomainCmd(),
		newPortRangeCmd(), newAuditCmd(), newConfigCmd(), newTokenCmd(), newProfileCmd(), newVersionCmd(),
	)
	return root
}

func configPath() string {
	if g.config != "" {
		return g.config
	}
	return config.Path(dataDir())
}

func dataDir() string {
	if g.dataDir != "" {
		return g.dataDir
	}
	return config.DefaultDataDir()
}

// loadServerConfig loads the server config file (or defaults).
func loadServerConfig() (*config.Config, error) {
	cfg, err := config.LoadOrEmpty(configPath())
	if err != nil {
		return nil, err
	}
	if g.dataDir != "" {
		cfg.DataDir = g.dataDir
		cfg.Normalize()
	}
	return cfg, nil
}

// backend opens local or remote administration.
func backend(ctx context.Context) (Backend, error) {
	if g.server != "" {
		// Subcommands may define their own --password (e.g. the new user's
		// initial password). Environment variables win as an explicit escape
		// hatch for remote administration in scripts.
		token := firstNonEmpty(g.token, os.Getenv("YUKI_TOKEN"))
		username := firstNonEmpty(g.username, os.Getenv("YUKI_USERNAME"))
		password := firstNonEmpty(g.password, os.Getenv("YUKI_PASSWORD"))
		return NewRemoteBackend(g.server, token, username, password, g.insecure)
	}
	cfg, err := loadServerConfig()
	if err != nil {
		return nil, err
	}
	return NewLocalBackend(cfg)
}

func printJSON(v any) error {
	return cliutil.NewPrinter(true).JSONOut(v)
}

func table(headers []string, rows [][]string) {
	cliutil.NewPrinter(false).Table(headers, rows)
}

func isJSON() bool { return g.jsonOut }

func confirm(prompt string) bool {
	if g.yes {
		return true
	}
	fmt.Print(prompt + " [y/N] ")
	var s string
	fmt.Scanln(&s)
	return strings.EqualFold(strings.TrimSpace(s), "y")
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本号",
		RunE: func(cmd *cobra.Command, args []string) error {
			if isJSON() {
				return printJSON(map[string]string{"version": versionString(), "mode": "server"})
			}
			fmt.Println("yuki-server " + versionString())
			return nil
		},
	}
}

// ensureDir makes the data directory with restrictive permissions.
func ensureDir(p string) error {
	if p == "" {
		return errors.New("数据目录不能为空")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0o700)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

var _ = context.Background
