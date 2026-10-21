package servercli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "user",
		Aliases: []string{"users"},
		Short:   "用户管理（创建、禁用、删除、密钥）",
	}
	cmd.AddCommand(
		newUserCreateCmd(), newUserListCmd(), newUserDisableCmd(),
		newUserEnableCmd(), newUserDeleteCmd(), newUserPasswordCmd(),
		newUserKeyCmd(),
	)
	return cmd
}

func newUserCreateCmd() *cobra.Command {
	var (
		password  string
		role      string
		display   string
		publicKey string
		keyFile   string
		keyName   string
	)
	cmd := &cobra.Command{
		Use:   "create <用户名>",
		Short: "创建用户（密码或 RSA 公钥）",
		Args:  cobra.ExactArgs(1),
		Example: `  yuki-server user create alice --password 'S3cure-pass'
  yuki-server user create bob --key-file ~/.ssh/id_rsa.pub --key-name bob-mac`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			key := publicKey
			if keyFile != "" {
				raw, err := readFile(keyFile)
				if err != nil {
					return err
				}
				key = raw
			}
			req := CreateUserRequest{
				Username: args[0], DisplayName: display, Password: password,
				Role: role, PublicKey: key, KeyName: keyName,
			}
			u, err := b.CreateUser(ctx, req)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "user": u})
			}
			fmt.Printf("✓ 已创建用户 %s（角色 %s）\n", u.Username, u.Role)
			if password == "" && key != "" {
				fmt.Println("  该用户使用 RSA Key 登录。")
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&password, "password", "p", "", "初始密码（至少 8 位）")
	cmd.Flags().StringVar(&password, "initial-password", "", "初始密码（别名，避免与全局 --password 冲突）")
	cmd.Flags().StringVar(&role, "role", "user", "角色：user 或 admin")
	cmd.Flags().StringVar(&display, "display-name", "", "显示名称")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "RSA/SSH 公钥内容（ssh-rsa AAAA...）")
	cmd.Flags().StringVar(&keyFile, "key-file", "", "公钥文件路径")
	cmd.Flags().StringVar(&keyName, "key-name", "", "密钥名称（便于识别设备）")
	return cmd
}

func newUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出用户",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			users, err := b.ListUsers(ctx)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"users": users})
			}
			rows := make([][]string, 0, len(users))
			for _, u := range users {
				rows = append(rows, []string{
					u.ID, u.Username, u.Role,
					map[bool]string{true: "禁用", false: "正常"}[u.Disabled],
					fmt.Sprint(u.KeyCount), fmt.Sprint(u.TunnelCount),
					localTime(u.LastLoginAt),
				})
			}
			table([]string{"ID", "用户名", "角色", "状态", "密钥", "隧道", "最近登录"}, rows)
			return nil
		},
	}
}

func newUserDisableCmd() *cobra.Command {
	return userToggleCmd("disable", "禁用用户（立即拒绝其登录）", false)
}

func newUserEnableCmd() *cobra.Command {
	return userToggleCmd("enable", "启用用户", true)
}

func userToggleCmd(use, short string, disabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <用户ID或用户名>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, name, err := resolveUser(ctx, b, args[0])
			if err != nil {
				return err
			}
			u, err := b.UpdateUser(ctx, id, UpdateUserRequest{Disabled: &disabled})
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "user": u})
			}
			fmt.Printf("✓ 用户 %s 已%s\n", name, map[bool]string{true: "启用", false: "禁用"}[!disabled])
			return nil
		},
	}
}

func newUserDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <用户ID或用户名>",
		Short: "删除用户及其隧道",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, name, err := resolveUser(ctx, b, args[0])
			if err != nil {
				return err
			}
			if !confirm(fmt.Sprintf("确认删除用户 %s？其隧道也会被删除", name)) {
				fmt.Println("已取消")
				return nil
			}
			if err := b.DeleteUser(ctx, id); err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "id": id})
			}
			fmt.Printf("✓ 已删除用户 %s\n", name)
			return nil
		},
	}
}

func newUserPasswordCmd() *cobra.Command {
	var password string
	cmd := &cobra.Command{
		Use:   "passwd <用户ID或用户名>",
		Short: "重置用户密码",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, name, err := resolveUser(ctx, b, args[0])
			if err != nil {
				return err
			}
			if password == "" {
				pw, err := promptPassword("新密码: ")
				if err != nil {
					return err
				}
				password = pw
			}
			u, err := b.UpdateUser(ctx, id, UpdateUserRequest{Password: &password})
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "user": u})
			}
			fmt.Printf("✓ 已重置 %s 的密码\n", name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&password, "password", "p", "", "新密码")
	return cmd
}

func newUserKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "key",
		Aliases: []string{"keys"},
		Short:   "管理用户的 RSA/SSH 公钥",
	}
	var (
		keyName   string
		keyFile   string
		publicKey string
	)
	addCmd := &cobra.Command{
		Use:   "add <用户ID或用户名>",
		Short: "为用户登记公钥",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, name, err := resolveUser(ctx, b, args[0])
			if err != nil {
				return err
			}
			key := publicKey
			if keyFile != "" {
				raw, err := readFile(keyFile)
				if err != nil {
					return err
				}
				key = raw
			}
			if key == "" {
				return errors.New("请提供 --key-file 或 --public-key")
			}
			k, err := b.AddKey(ctx, id, keyName, key)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true, "key": k})
			}
			fmt.Printf("✓ 已为 %s 登记公钥：%s\n", name, k.Fingerprint)
			return nil
		},
	}
	addCmd.Flags().StringVar(&keyName, "key-name", "", "密钥名称")
	addCmd.Flags().StringVar(&keyFile, "key-file", "", "公钥文件")
	addCmd.Flags().StringVar(&publicKey, "public-key", "", "公钥内容")

	listCmd := &cobra.Command{
		Use:   "list <用户ID或用户名>",
		Short: "列出用户公钥",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, _, err := resolveUser(ctx, b, args[0])
			if err != nil {
				return err
			}
			keys, err := b.ListKeys(ctx, id)
			if err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"keys": keys})
			}
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				rows = append(rows, []string{k.ID, k.Name, k.Fingerprint, k.CreatedAt.Format("2006-01-02")})
			}
			table([]string{"ID", "名称", "指纹", "登记时间"}, rows)
			return nil
		},
	}
	rmCmd := &cobra.Command{
		Use:   "remove <用户ID或用户名> <密钥ID>",
		Short: "删除用户公钥",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			b, err := backend(ctx)
			if err != nil {
				return err
			}
			defer b.Close()
			id, _, err := resolveUser(ctx, b, args[0])
			if err != nil {
				return err
			}
			if err := b.DeleteKey(ctx, id, args[1]); err != nil {
				return err
			}
			if isJSON() {
				return printJSON(map[string]any{"ok": true})
			}
			fmt.Println("✓ 已删除密钥")
			return nil
		},
	}
	cmd.AddCommand(addCmd, listCmd, rmCmd)
	return cmd
}

// resolveUser accepts an id or a username and returns (id, username).
func resolveUser(ctx context.Context, b Backend, arg string) (string, string, error) {
	users, err := b.ListUsers(ctx)
	if err != nil {
		return "", "", err
	}
	for _, u := range users {
		if u.ID == arg || u.Username == arg {
			return u.ID, u.Username, nil
		}
	}
	return "", "", fmt.Errorf("找不到用户 %q", arg)
}
