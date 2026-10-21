package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yuuuuukiu/yukitunnel/internal/cliutil"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "server",
		Aliases: []string{"servers", "node", "nodes"},
		Short:   "查看可用的穿透节点（含延迟）",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出节点、在线状态和延迟",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := requireLogin()
			if err != nil {
				return err
			}
			res, err := cliutil.ListServers(cfg)
			if err != nil {
				return err
			}
			if flags.jsonOut {
				return printer().JSONOut(res)
			}
			if len(res.Servers) == 0 {
				fmt.Println("服务端暂未提供可用节点，请联系管理员。")
				return nil
			}
			rows := make([][]string, 0, len(res.Servers))
			for _, s := range res.Servers {
				status := "离线"
				if s.Online {
					status = "在线"
				}
				rows = append(rows, []string{
					s.ID, s.Name, s.Region, s.PublicHost, status,
					fmt.Sprintf("%d ms", s.LatencyMS),
					fmt.Sprintf("%d-%d", s.PortRange[0], s.PortRange[1]),
				})
			}
			printer().Table([]string{"ID", "名称", "区域", "公网入口", "状态", "延迟", "端口范围"}, rows)
			fmt.Printf("\n域名绑定：%s", cliutil.YesNo(res.DomainBinding))
			if res.DomainSuffix != "" {
				fmt.Printf("（后缀 %s）", res.DomainSuffix)
			}
			fmt.Println()
			return nil
		},
	})
	return cmd
}
