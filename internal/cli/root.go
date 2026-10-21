// Package cli implements the `yuki` client command tree.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
	"github.com/yuuuuukiu/yukitunnel/internal/version"
)

type globalFlags struct {
	jsonOut  bool
	server   string
	token    string
	insecure bool
	keyPath  string
	user     string
	verbose  bool
}

var flags globalFlags

// Execute runs the CLI.
func Execute(args []string) error {
	root := newRoot()
	root.SetArgs(args)
	return root.Execute()
}

// Render turns an error into a human message.
func Render(err error) string {
	return cliutil.RenderError(err)
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "yuki",
		Short: "YukiTunnel 客户端：创建并启动内网穿透隧道",
		Long: `YukiTunnel 客户端。

第一次使用：
  yuki login --server https://your-server:8443 --username alice
  yuki server list
  yuki tunnel create --node <节点ID> --proto tcp --local-port 8080
  yuki tunnel start <隧道ID>

所有命令都支持 --json，便于脚本调用。`,
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&flags.jsonOut, "json", false, "以 JSON 输出，便于脚本处理")
	pf.StringVarP(&flags.server, "server", "s", "", "服务端地址，例如 https://panel.example.com:8443")
	pf.StringVar(&flags.token, "token", "", "使用 API 令牌（覆盖本地登录状态）")
	pf.BoolVar(&flags.insecure, "insecure", false, "跳过 TLS 证书校验（自签名证书）")
	pf.StringVar(&flags.keyPath, "key", "", "RSA 私钥路径（SSH Key 认证）")
	pf.StringVarP(&flags.user, "username", "u", "", "用户名")
	pf.BoolVarP(&flags.verbose, "verbose", "v", false, "输出详细日志")

	root.AddCommand(
		newLoginCmd(), newLogoutCmd(), newWhoamiCmd(),
		newServerCmd(), newTunnelCmd(), newConfigCmd(),
		newRunCmd(), newStatusCmd(), newDoctorCmd(),
	)
	return root
}

// loadConfig loads the profile and applies global flags.
func loadConfig() (*cliutil.ClientConfig, error) {
	cfg, err := cliutil.LoadClientConfig()
	if err != nil {
		return nil, err
	}
	if flags.server != "" {
		cfg.ServerURL = cliutil.NormalizeServerURL(flags.server)
	}
	if flags.token != "" {
		cfg.Token = flags.token
	}
	if flags.insecure {
		cfg.InsecureSkipVerify = true
	}
	if flags.keyPath != "" {
		cfg.KeyPath = flags.keyPath
	}
	if cfg.ServerURL == "" {
		return nil, errors.New("尚未配置服务端地址，请先运行：yuki login --server https://你的服务端:8443 --username 用户名")
	}
	if cfg.Token == "" && cfg.KeyPath == "" && flags.user == "" {
		return nil, errors.New("尚未登录，请先运行：yuki login")
	}
	return cfg, nil
}

// requireLogin loads a config with a usable token.
func requireLogin() (*cliutil.ClientConfig, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Token == "" {
		return nil, errors.New("尚未登录或登录已过期，请运行：yuki login")
	}
	return cfg, nil
}

func printer() *cliutil.Printer { return cliutil.NewPrinter(flags.jsonOut) }

func printErr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func normalizeNodeArg(s string) string { return strings.TrimSpace(s) }
