package servercli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newTunnelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tunnel",
		Aliases: []string{"tunnels"},
		Short:   "隧道管理（查看、启用、禁用、删除）",
	}
	cmd.AddCommand(
		newTunnelListCmd(), newTunnelEnableCmd(), newTunnelDisableCmd(), newTunnelDeleteCmd(),
	)
	return cmd
}

func newTunnelListCmd() *cobra.Command {
	var user string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出所有隧道",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			filter := user
			if filter != "" {
				id, _, err := resolveUser(ctx, b, filter)
				if err != nil {
					return err
				}
				filter = id
			}
			list, err := b.ListTunnels(ctx, filter)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"tunnels": list})
			}
			rows := make([][]string, 0, len(list))
			for _, t := range list {
				rows = append(rows, []string{
					t.ID, t.UserName, t.Name, t.Proto,
					fmt.Sprintf("%s:%d", t.LocalIP, t.LocalPort), t.Address,
					map[bool]string{true: "启用", false: "禁用"}[t.Enabled],
					map[bool]string{true: "运行中", false: "已停止"}[t.Active],
				})
			}
			table([]string{"ID", "用户", "名称", "协议", "内网", "公网地址", "开关", "运行"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVarP(&user, "user", "u", "", "只显示某个用户的隧道")
	return cmd
}

func tunnelToggle(use, short string, enabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <隧道ID>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			t, err := b.EnableTunnel(ctx, args[0], enabled)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "tunnel": t})
			}
			fmt.Printf("✓ 隧道 %s 已%s\n", t.Name, map[bool]string{true: "启用", false: "禁用"}[enabled])
			return nil
		},
	}
}

func newTunnelEnableCmd() *cobra.Command { return tunnelToggle("enable", "启用隧道", true) }
func newTunnelDisableCmd() *cobra.Command {
	return tunnelToggle("disable", "禁用隧道（立即停止）", false)
}

func newTunnelDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <隧道ID>",
		Short: "删除隧道",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			if !confirm(fmt.Sprintf("确认删除隧道 %s？", args[0])) {
				fmt.Println("已取消")
				return nil
			}
			if err := b.DeleteTunnel(ctx, args[0]); err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "id": args[0]})
			}
			fmt.Println("✓ 已删除隧道")
			return nil
		},
	}
}
