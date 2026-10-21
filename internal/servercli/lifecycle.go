package servercli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/config"
	"github.com/yuuuuukiu/yukitunnel/internal/firewall"
	"github.com/yuuuuukiu/yukitunnel/internal/server"
)

// pidFile returns the path of the background server pid file.
func pidFile() string { return filepath.Join(dataDir(), "yuki-server.pid") }

func writePID(pid int) error {
	return os.WriteFile(pidFile(), []byte(strconv.Itoa(pid)), 0o600)
}

func readPID() (int, error) {
	b, err := os.ReadFile(pidFile())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "在前台运行服务端（容器入口使用）",
		Hidden: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadServerConfig()
			if err != nil {
				return err
			}
			level := slog.LevelInfo
			if cfg.Log.Level == "debug" {
				level = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
			svc, err := server.New(cfg, logger)
			if err != nil {
				return err
			}
			if err := svc.Start(); err != nil {
				_ = svc.Shutdown(context.Background())
				return err
			}
			_ = writePID(os.Getpid())
			defer os.Remove(pidFile())
			logger.Info("YukiTunnel server started", "api", cfg.APIAddr, "control", cfg.ControlAddr, "data", cfg.DataDir)
			if isJSON() {
				_ = printJSON(map[string]any{"status": "started", "api": cfg.APIAddr, "pid": os.Getpid()})
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-ctx.Done()
			logger.Info("shutting down")
			shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return svc.Shutdown(shut)
		},
	}
}

func newStartCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "启动服务端",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadServerConfig()
			if err != nil {
				return err
			}
			if !foreground {
				if pid, err := readPID(); err == nil && processAlive(pid) {
					return fmt.Errorf("服务端已在运行（PID %d）", pid)
				}
			}
			if err := healthCheck(cfg); err == nil {
				return fmt.Errorf("端口 %s 上已有 YukiTunnel 服务端在运行", cfg.APIAddr)
			}
			if !foreground {
				return startBackground(cfg)
			}
			return runForeground(cfg)
		},
	}
	cmd.Flags().BoolVarP(&foreground, "foreground", "f", false, "前台运行（systemd/docker 使用）")
	return cmd
}

func runForeground(cfg *config.Config) error {
	level := slog.LevelInfo
	if cfg.Log.Level == "debug" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	svc, err := server.New(cfg, logger)
	if err != nil {
		return err
	}
	if err := svc.Start(); err != nil {
		return err
	}
	_ = writePID(os.Getpid())
	defer os.Remove(pidFile())
	fmt.Printf("✓ 服务端已启动：https://%s（Ctrl+C 停止）\n", cfg.APIAddr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return svc.Shutdown(shut)
}

func startBackground(cfg *config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(cfg.DataDir, "logs")
	_ = os.MkdirAll(logPath, 0o700)
	logFile := filepath.Join(logPath, "server.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	proc, err := os.StartProcess(exe, []string{exe, "serve", "--data-dir", cfg.DataDir}, &os.ProcAttr{
		Files: []*os.File{nil, f, f},
		Sys:   detachedProcAttr(),
	})
	if err != nil {
		return fmt.Errorf("启动后台进程失败：%w", err)
	}
	_ = writePID(proc.Pid)
	// Wait for health.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := healthCheck(cfg); err == nil {
			if isJSON() {
				return printJSON(map[string]any{"status": "running", "pid": proc.Pid, "api": cfg.APIAddr, "log": logFile})
			}
			fmt.Printf("✓ 服务端已启动（PID %d）\n", proc.Pid)
			fmt.Printf("  WebUI ：https://%s\n", cfg.APIAddr)
			fmt.Printf("  日志  ：%s\n", logFile)
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("服务端启动超时，请查看日志 %s", logFile)
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "停止服务端",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := readPID()
			if err != nil {
				return errors.New("未找到运行中的服务端（PID 文件不存在）")
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			_ = proc.Signal(syscall.SIGTERM)
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if !processAlive(pid) {
					_ = os.Remove(pidFile())
					if isJSON() {
						return printJSON(map[string]any{"status": "stopped", "pid": pid})
					}
					fmt.Println("✓ 服务端已停止")
					return nil
				}
				time.Sleep(300 * time.Millisecond)
			}
			_ = proc.Kill()
			_ = os.Remove(pidFile())
			return errors.New("服务端未在超时内退出，已强制结束")
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "重启服务端",
		RunE: func(cmd *cobra.Command, args []string) error {
			if pid, err := readPID(); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Signal(syscall.SIGTERM)
					time.Sleep(1500 * time.Millisecond)
				}
			}
			cfg, err := loadServerConfig()
			if err != nil {
				return err
			}
			return startBackground(cfg)
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "显示服务端运行状态与健康检查",
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.server != "" {
				b, err := backend(cmd.Context())
				if err != nil {
					return err
				}
				h, err := b.Health(cmd.Context())
				alive := err == nil
				out := map[string]any{
					"mode":    "remote",
					"server":  g.server,
					"running": alive,
					"healthy": alive,
				}
				if err != nil {
					out["health_error"] = err.Error()
				} else {
					for k, v := range h {
						out[k] = v
					}
				}
				if isJSON() {
					return printJSON(out)
				}
				fmt.Printf("管理模式：远程 (server: %s)\n", g.server)
				fmt.Printf("运行状态：%s\n", map[bool]string{true: "运行中", false: "未响应"}[alive])
				if err == nil {
					fmt.Println("健康检查：✓ 正常")
					if ver, ok := h["version"].(string); ok && ver != "" {
						fmt.Printf("版本    ：%s\n", ver)
					}
				} else {
					fmt.Printf("健康检查：✗ %s\n", err)
				}
				return nil
			}
			cfg, err := loadServerConfig()
			if err != nil {
				return err
			}
			pid, _ := readPID()
			alive := pid > 0 && processAlive(pid)
			hcErr := healthCheck(cfg)
			out := map[string]any{
				"running": alive, "pid": pid, "api": cfg.APIAddr,
				"healthy": hcErr == nil, "data_dir": cfg.DataDir,
			}
			if hcErr != nil {
				out["health_error"] = hcErr.Error()
			}
			if isJSON() {
				return printJSON(out)
			}
			fmt.Printf("运行状态：%s\n", map[bool]string{true: "运行中", false: "未运行"}[alive])
			if pid > 0 {
				fmt.Printf("PID     ：%d\n", pid)
			}
			fmt.Printf("API     ：https://%s\n", cfg.APIAddr)
			if hcErr == nil {
				fmt.Println("健康检查：✓ 正常")
			} else {
				fmt.Printf("健康检查：✗ %s\n", hcErr)
			}
			fmt.Printf("数据目录：%s\n", cfg.DataDir)
			return nil
		},
	}
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "环境检测与健康检查（部署失败诊断）",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadServerConfig()
			if err != nil {
				return err
			}
			type check struct {
				Name   string `json:"name"`
				OK     bool   `json:"ok"`
				Detail string `json:"detail"`
			}
			var checks []check
			add := func(name string, ok bool, detail string) {
				checks = append(checks, check{Name: name, OK: ok, Detail: detail})
			}
			// data dir
			if fi, err := os.Stat(cfg.DataDir); err != nil || !fi.IsDir() {
				add("数据目录", false, "不存在或不可访问："+cfg.DataDir)
			} else {
				probe := filepath.Join(cfg.DataDir, ".write-test")
				if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
					add("数据目录", false, "不可写："+err.Error())
				} else {
					_ = os.Remove(probe)
					add("数据目录", true, cfg.DataDir)
				}
			}
			// config
			if _, err := os.Stat(configPath()); err != nil {
				add("配置文件", false, "缺少 "+configPath()+"，请先执行 yuki-server init")
			} else {
				add("配置文件", true, configPath())
			}
			// TLS
			if _, err := os.Stat(cfg.TLS.CertFile); err != nil {
				add("TLS 证书", false, "证书文件缺失："+cfg.TLS.CertFile)
			} else if fp, err := tlsFingerprint(cfg.TLS.CertFile); err != nil {
				add("TLS 证书", false, err.Error())
			} else {
				add("TLS 证书", true, fp)
			}
			// ports
			serverRunning := healthCheck(cfg) == nil
			for _, addr := range []string{cfg.APIAddr, cfg.ControlAddr} {
				l, err := net.Listen("tcp", addr)
				if err != nil {
					// When our own server is healthy, an occupied port is expected.
					if serverRunning {
						add("端口 "+addr, true, "已由运行中的服务端占用")
						continue
					}
					add("端口 "+addr, false, err.Error()+"（可能被其他程序占用）")
					continue
				}
				_ = l.Close()
				add("端口 "+addr, true, "可用")
			}
			// database
			if _, err := os.Stat(filepath.Join(cfg.DataDir, "yukitunnel.db")); err != nil {
				add("数据库", false, "尚未初始化")
			} else {
				add("数据库", true, filepath.Join(cfg.DataDir, "yukitunnel.db"))
			}
			// health endpoint
			if err := healthCheck(cfg); err != nil {
				add("健康检查", false, err.Error())
			} else {
				add("健康检查", true, "https://"+cfg.APIAddr+"/api/v1/health")
			}
			// port range
			if cfg.Ports.Min >= 1 && cfg.Ports.Max <= 65535 && cfg.Ports.Min <= cfg.Ports.Max {
				add("端口范围", true, fmt.Sprintf("%d-%d", cfg.Ports.Min, cfg.Ports.Max))
			} else {
				add("端口范围", false, "配置无效")
			}
			// host firewall
			fwSvc := firewall.NewService(slog.Default(), os.Getenv("YUKI_FIREWALL") == "off")
			fwStatus := fwSvc.Status(context.Background())
			fwDetail := fwSvc.Summary(context.Background())
			fwOK := fwStatus.Type == firewall.TypeUnsupported || fwStatus.Type == firewall.TypeNone ||
				!fwStatus.Enabled || fwStatus.DefaultInboundPolicy != firewall.PolicyDeny || fwStatus.Managed
			add("系统防火墙", fwOK, fwDetail)
			if fwStatus.Type != firewall.TypeUnsupported && fwStatus.Type != firewall.TypeNone &&
				fwStatus.Enabled && fwStatus.DefaultInboundPolicy == firewall.PolicyDeny && !fwStatus.Managed {
				add("防火墙权限", false, "入站默认 deny 但当前用户无权限修改；请用 sudo 运行，或手工放行端口段")
			}
			allOK := true
			for _, c := range checks {
				if !c.OK {
					allOK = false
				}
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": allOK, "checks": checks})
			}
			for _, c := range checks {
				icon := "✓"
				if !c.OK {
					icon = "✗"
				}
				fmt.Printf("%s %-14s %s\n", icon, c.Name, c.Detail)
			}
			if !allOK {
				fmt.Println("\n存在未通过项。常见修复：")
				fmt.Println("  · 端口被占用 → 修改 config.yaml 的 api_addr / control_addr 或释放端口")
				fmt.Println("  · 权限不足  → 使用 sudo，或把数据目录改到用户可写位置")
				fmt.Println("  · 未初始化  → 运行 yuki-server init")
				os.Exit(1)
			}
			return nil
		},
	}
}

func tlsFingerprint(certFile string) (string, error) {
	b, err := os.ReadFile(certFile)
	if err != nil {
		return "", err
	}
	return fingerprintPEM(b)
}

// healthCheck probes the local API health endpoint.
func healthCheck(cfg *config.Config) error {
	addr := cfg.APIAddr
	if strings.HasPrefix(addr, "0.0.0.0") {
		_, port, _ := net.SplitHostPort(addr)
		addr = "127.0.0.1:" + port
	}
	client := insecureClient(4 * time.Second)
	resp, err := client.Get("https://" + addr + "/api/v1/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("健康检查返回 %d", resp.StatusCode)
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
