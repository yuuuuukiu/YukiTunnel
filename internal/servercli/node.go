package servercli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "node",
		Aliases: []string{"nodes"},
		Short:   "节点（公网入口）管理",
	}
	cmd.AddCommand(
		newNodeCreateCmd(), newNodeListCmd(), newNodeEnableCmd(),
		newNodeDisableCmd(), newNodeDeleteCmd(), newNodeCheckCmd(),
	)
	return cmd
}

func newNodeCreateCmd() *cobra.Command {
	var (
		region      string
		publicHost  string
		controlHost string
		controlPort int
		maxTunnels  int
	)
	cmd := &cobra.Command{
		Use:   "create <节点名称>",
		Short: "添加穿透节点（公网入口）",
		Args:  cobra.ExactArgs(1),
		Example: `  yuki-server node create hk-1 --public-host 203.0.113.10 --control-port 9443 --region hk
  yuki-server node create jp-1 --public-host jp.example.com --region jp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			enabled := true
			n, err := b.CreateNode(ctx, NodeRequest{
				Name: args[0], Region: region, PublicHost: publicHost,
				ControlHost: controlHost, ControlPort: controlPort, MaxTunnels: maxTunnels,
				Enabled: &enabled,
			})
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "node": n})
			}
			fmt.Printf("✓ 已添加节点 %s（ID %s）\n", n.Name, n.ID)
			fmt.Printf("  公网入口：%s\n", n.PublicHost)
			return nil
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "区域标签，例如 hk / jp / us")
	cmd.Flags().StringVar(&publicHost, "public-host", "", "公网 IP 或域名（必填）")
	cmd.Flags().StringVar(&controlHost, "control-host", "", "控制端口所在主机（默认同 public-host）")
	cmd.Flags().IntVar(&controlPort, "control-port", 9443, "控制端口")
	cmd.Flags().IntVar(&maxTunnels, "max-tunnels", 0, "该节点最大隧道数（0 为不限）")
	_ = cmd.MarkFlagRequired("public-host")
	return cmd
}

func newNodeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出节点及在线状态、延迟",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			nodes, err := b.ListNodes(ctx)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"nodes": nodes})
			}
			rows := make([][]string, 0, len(nodes))
			for _, n := range nodes {
				rows = append(rows, []string{
					n.ID, n.Name, n.Region, n.PublicHost,
					map[bool]string{true: "启用", false: "停用"}[n.Enabled],
					n.Status, fmt.Sprintf("%d ms", n.LatencyMS),
				})
			}
			table([]string{"ID", "名称", "区域", "公网入口", "开关", "状态", "延迟"}, rows)
			return nil
		},
	}
}

func nodeToggle(use, short string, enabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <节点ID或名称>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, name, err := resolveNode(ctx, b, args[0])
			if err != nil {
				return err
			}
			n, err := b.UpdateNode(ctx, id, NodeRequest{Enabled: &enabled})
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "node": n})
			}
			fmt.Printf("✓ 节点 %s 已%s\n", name, map[bool]string{true: "启用", false: "停用"}[enabled])
			return nil
		},
	}
}

func newNodeEnableCmd() *cobra.Command { return nodeToggle("enable", "启用节点", true) }
func newNodeDisableCmd() *cobra.Command {
	return nodeToggle("disable", "停用节点（其隧道将停止）", false)
}

func newNodeDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <节点ID或名称>",
		Short: "删除节点",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, name, err := resolveNode(ctx, b, args[0])
			if err != nil {
				return err
			}
			if !confirm(fmt.Sprintf("确认删除节点 %s？", name)) {
				fmt.Println("已取消")
				return nil
			}
			if err := b.DeleteNode(ctx, id); err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "id": id})
			}
			fmt.Printf("✓ 已删除节点 %s\n", name)
			return nil
		},
	}
}

func newNodeCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check <节点ID或名称>",
		Short: "检测节点连通性与延迟",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, _, err := resolveNode(ctx, b, args[0])
			if err != nil {
				return err
			}
			n, err := b.CheckNode(ctx, id)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(n)
			}
			fmt.Printf("节点 %s：%s，延迟 %d ms\n", n.ID, n.Status, n.LatencyMS)
			return nil
		},
	}
}

func resolveNode(ctx context.Context, b Backend, arg string) (string, string, error) {
	nodes, err := b.ListNodes(ctx)
	if err != nil {
		return "", "", err
	}
	for _, n := range nodes {
		if n.ID == arg || n.Name == arg {
			return n.ID, n.Name, nil
		}
	}
	return "", "", fmt.Errorf("找不到节点 %q", arg)
}
