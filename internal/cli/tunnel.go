package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
)

func newTunnelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tunnel",
		Aliases: []string{"tunnels", "t"},
		Short:   "创建、查看、启动和停止隧道",
	}
	cmd.AddCommand(
		newTunnelCreateCmd(), newTunnelListCmd(), newTunnelStartCmd(),
		newTunnelStopCmd(), newTunnelDeleteCmd(), newTunnelLogsCmd(),
	)
	return cmd
}

func newTunnelCreateCmd() *cobra.Command {
	var (
		nodeID     string
		proto      string
		localIP    string
		localPort  int
		publicPort int
		name       string
		useDomain  bool
		noDomain   bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "创建一条隧道",
		Long:  `创建隧道。节点可通过 ` + "`yuki server list`" + ` 查看；不指定 --node 时使用第一个在线节点。`,
		Example: `  yuki tunnel create --proto tcp --local-port 8080
  yuki tunnel create --proto http --local-ip 192.168.1.10 --local-port 80 --node n1a2b3c4
  yuki tunnel create --proto udp --local-port 25565 --public-port 25565`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			servers, err := cliutil.ListServers(cfg)
			if err != nil {
				return err
			}
			if len(servers.Servers) == 0 {
				return errors.New("服务端没有可用节点，请联系管理员添加节点")
			}
			chosen := pickNode(servers.Servers, nodeID)
			if chosen == nil {
				return fmt.Errorf("找不到节点 %q，请运行 yuki server list 查看可用节点", nodeID)
			}
			if localPort == 0 {
				localPort = 80
				if proto == "https" {
					localPort = 443
				}
			}
			if localIP == "" {
				localIP = "127.0.0.1"
			}
			req := cliutil.CreateTunnelRequest{
				Name: name, NodeID: chosen.ID, Proto: proto, LocalIP: localIP,
				LocalPort: localPort, PublicPort: publicPort,
			}
			if noDomain {
				v := false
				req.UseDomain = &v
			} else if useDomain {
				v := true
				req.UseDomain = &v
			}
			t, err := cliutil.CreateTunnel(cfg, req)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(map[string]any{"tunnel": t, "address": t.Address})
			}
			fmt.Printf("✓ 隧道创建成功：%s\n", t.Name)
			fmt.Printf("  隧道 ID ：%s\n", t.ID)
			fmt.Printf("  节点    ：%s\n", t.NodeName)
			fmt.Printf("  协议    ：%s\n", strings.ToUpper(t.Proto))
			fmt.Printf("  内网    ：%s:%d\n", t.LocalIP, t.LocalPort)
			fmt.Printf("  公网地址：%s\n", t.Address)
			fmt.Printf("\n运行 `yuki tunnel start %s` 启动穿透。\n", t.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&nodeID, "node", "", "节点 ID（默认第一个在线节点）")
	cmd.Flags().StringVar(&proto, "proto", "tcp", "协议：tcp / udp / http / https")
	cmd.Flags().StringVar(&localIP, "local-ip", "127.0.0.1", "内网 IP 或主机名")
	cmd.Flags().IntVar(&localPort, "local-port", 0, "内网端口")
	cmd.Flags().IntVar(&publicPort, "public-port", 0, "指定公网端口（默认自动分配）")
	cmd.Flags().StringVar(&name, "name", "", "隧道名称")
	cmd.Flags().BoolVar(&useDomain, "domain", false, "使用域名地址（需管理员开启域名绑定）")
	cmd.Flags().BoolVar(&noDomain, "no-domain", false, "强制使用 IP 地址")
	return cmd
}

func pickNode(list []cliutil.ServerView, want string) *cliutil.ServerView {
	if want != "" {
		for i := range list {
			if list[i].ID == want || list[i].Name == want {
				return &list[i]
			}
		}
		return nil
	}
	var best *cliutil.ServerView
	for i := range list {
		if !list[i].Online {
			continue
		}
		if best == nil || list[i].LatencyMS < best.LatencyMS {
			best = &list[i]
		}
	}
	if best == nil && len(list) > 0 {
		return &list[0]
	}
	return best
}

func newTunnelListCmd() *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出当前用户的隧道",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			for {
				list, err := cliutil.ListTunnels(cfg)
				if err != nil {
					return err
				}
				if flags.jsonOut {
					if err := printer().JSONOut(map[string]any{"tunnels": list}); err != nil {
						return err
					}
				} else {
					printTunnelTable(list)
				}
				if !watch {
					return nil
				}
				time.Sleep(3 * time.Second)
				fmt.Print("\033[H\033[2J")
			}
		},
	}
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "持续刷新状态")
	return cmd
}

func printTunnelTable(list []cliutil.Tunnel) {
	if len(list) == 0 {
		fmt.Println("还没有隧道。运行 `yuki tunnel create --proto tcp --local-port 8080` 创建第一条。")
		return
	}
	w := tabwriter.NewWriter(stdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\t名称\t协议\t内网地址\t公网地址\t状态\t流量")
	for _, t := range list {
		status := "已停止"
		if t.Active {
			status = "运行中"
		}
		if !t.Enabled {
			status = "已禁用"
		}
		traffic := fmt.Sprintf("↑%s ↓%s", cliutil.HumanBytes(t.RxBytes), cliutil.HumanBytes(t.TxBytes))
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Name, strings.ToUpper(t.Proto),
			fmt.Sprintf("%s:%d", t.LocalIP, t.LocalPort), t.Address, status, traffic)
	}
	_ = w.Flush()
}

func newTunnelStartCmd() *cobra.Command {
	var detach bool
	cmd := &cobra.Command{
		Use:   "start <隧道ID>",
		Short: "启动隧道",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			id := normalizeNodeArg(args[0])
			t, err := cliutil.TunnelAction(cfg, id, "start")
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(map[string]any{"ok": true, "tunnel": t, "address": t.Address})
			}
			fmt.Printf("✓ 隧道已启动：%s\n", t.Address)
			if !detach {
				fmt.Println("提示：本命令只通知服务端启动隧道；若要持续转发流量，请保持 `yuki run` 运行，或使用桌面客户端。")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&detach, "quiet", false, "不打印提示")
	return cmd
}

func newTunnelStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <隧道ID>",
		Short: "停止隧道",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			t, err := cliutil.TunnelAction(cfg, args[0], "stop")
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(map[string]any{"ok": true, "tunnel": t})
			}
			fmt.Printf("✓ 隧道已停止：%s\n", t.Name)
			return nil
		},
	}
}

func newTunnelDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <隧道ID>",
		Aliases: []string{"rm", "remove"},
		Short:   "删除隧道",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			id := args[0]
			if !yes && !flags.jsonOut {
				if strings.ToLower(prompt(fmt.Sprintf("确认删除隧道 %s？[y/N] ", id))) != "y" {
					fmt.Println("已取消")
					return nil
				}
			}
			if err := cliutil.DeleteTunnel(cfg, id); err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(map[string]any{"ok": true, "id": id})
			}
			fmt.Printf("✓ 已删除隧道 %s\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "跳过确认")
	return cmd
}

func newTunnelLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats <隧道ID>",
		Short: "查看隧道流量与连接数",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			c := cliutil.NewClient(cfg)
			var out struct {
				Active      bool  `json:"active"`
				RxBytes     int64 `json:"rx_bytes"`
				TxBytes     int64 `json:"tx_bytes"`
				Connections int64 `json:"connections"`
				ActiveConns int   `json:"active_connections"`
			}
			if err := c.Do("GET", "/tunnels/"+args[0]+"/stats", nil, &out); err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(out)
			}
			fmt.Printf("状态：%s\n", cliutil.StatusDot(out.Active))
			fmt.Printf("上行：%s\n", cliutil.HumanBytes(out.RxBytes))
			fmt.Printf("下行：%s\n", cliutil.HumanBytes(out.TxBytes))
			fmt.Printf("累计连接：%d\n", out.Connections)
			fmt.Printf("当前连接：%d\n", out.ActiveConns)
			return nil
		},
	}
}

var _ = strconv.Itoa
