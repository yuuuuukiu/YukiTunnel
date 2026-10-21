package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/client"
	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
)

func newRunCmd() *cobra.Command {
	var (
		all      bool
		only     []string
		headless bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "在前台保持连接，自动启动并转发隧道",
		Long: `连接服务端并保持长连接。默认启动全部已创建的隧道；
也可用 --tunnel 只启动指定隧道。断线后自动重连。`,
		Example: `  yuki run
  yuki run --tunnel tun_ab12cd34
  yuki run --all --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runAgent(ctx, cfg, all || len(only) == 0, only, headless)
		},
	}
	cmd.Flags().BoolVar(&all, "all", true, "启动全部隧道")
	cmd.Flags().StringSliceVar(&only, "tunnel", nil, "只启动指定隧道 ID（可重复）")
	cmd.Flags().BoolVar(&headless, "headless", false, "只输出 JSON 日志，适合 systemd")
	return cmd
}

func runAgent(ctx context.Context, cfg *cliutil.ClientConfig, all bool, only []string, headless bool) error {
	tunnels, err := cliutil.ListTunnels(cfg)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	selected := make([]cliutil.Tunnel, 0, len(tunnels))
	for _, t := range tunnels {
		if !t.Enabled {
			continue
		}
		if all || want[t.ID] {
			selected = append(selected, t)
		}
	}
	if len(selected) == 0 {
		return errors.New("没有可启动的隧道，请先运行 yuki tunnel create")
	}

	// The CLI renders logs through OnLog; keep the slog handler quiet unless -v.
	level := slog.LevelError
	if flags.verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	agent := client.New(client.Options{
		ServerURL:          cfg.ServerURL,
		Token:              cfg.Token,
		KeyPath:            cfg.KeyPath,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		Fingerprint:        cfg.Fingerprint,
		Logger:             logger,
		AutoReconnect:      cfg.AutoReconnect,
		OnLog: func(e client.LogEntry) {
			if headless || flags.jsonOut {
				fmt.Fprintf(os.Stderr, "%s [%s] %s\n", e.Time.Format("15:04:05"), strings.ToUpper(string(e.Level)), e.Message)
				return
			}
			icon := "·"
			switch e.Level {
			case client.LogWarn:
				icon = "!"
			case client.LogError:
				icon = "✗"
			}
			fmt.Fprintf(os.Stderr, "%s %s %s\n", icon, e.Time.Format("15:04:05"), e.Message)
		},
		OnState: func(state string) {
			if state == "connected" && flags.verbose {
				fmt.Fprintln(os.Stderr, "✓ 已连接到服务端")
			}
		},
		OnStats: func(id string, rx, tx int64, active int) {
			if flags.jsonOut {
				fmt.Fprintf(os.Stderr, `{"tunnel":%q,"rx":%d,"tx":%d,"active":%d}`+"\n", id, rx, tx, active)
			}
		},
	})

	if err := agent.StartTunnels(selected); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "正在连接 %s，共 %d 条隧道…\n", cfg.ServerURL, len(selected))
	err = agent.Run(ctx)
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "已退出")
		return nil
	}
	return err
}

// startSpecs converts API tunnels to data-plane specs.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "显示登录状态、服务端连通性和隧道概况",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliutil.LoadClientConfig()
			if err != nil {
				return err
			}
			if flags.server != "" {
				cfg.ServerURL = cliutil.NormalizeServerURL(flags.server)
			}
			out := map[string]any{"server": cfg.ServerURL, "logged_in": cfg.Token != ""}
			if cfg.Token == "" || cfg.ServerURL == "" {
				if flags.jsonOut {
					return printer().JSONOut(out)
				}
				fmt.Println("未登录。运行 `yuki login` 开始。")
				return nil
			}
			c := cliutil.NewClient(cfg)
			var meta map[string]any
			start := time.Now()
			err = c.Do("GET", "/me", nil, &meta)
			out["reachable"] = err == nil
			out["latency_ms"] = time.Since(start).Milliseconds()
			if err != nil {
				out["error"] = cliutil.RenderError(err)
			} else {
				out["user"] = meta["user"]
			}
			if list, lerr := cliutil.ListTunnels(cfg); lerr == nil {
				active := 0
				for _, t := range list {
					if t.Active {
						active++
					}
				}
				out["tunnels"] = len(list)
				out["active_tunnels"] = active
			}
			if flags.jsonOut {
				return printer().JSONOut(out)
			}
			fmt.Printf("服务端  ：%s\n", cfg.ServerURL)
			if err != nil {
				fmt.Printf("连通性  ：✗ %s\n", cliutil.RenderError(err))
				return nil
			}
			fmt.Printf("连通性  ：✓ %d ms\n", time.Since(start).Milliseconds())
			fmt.Printf("隧道    ：%v 条，运行中 %v 条\n", out["tunnels"], out["active_tunnels"])
			return nil
		},
	}
}
