package agent

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/FlintyLemming/orciny"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "orciny-agent",
		Short:         "Orciny agent —— 常驻本机，向 hub 汇报状态",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().String("dir", "", "agent 数据目录（默认 $ORCINY_HOME 或 ~/.orciny）")
	root.AddCommand(newVersionCmd())
	root.AddCommand(newEnrollCmd())
	// run / status 由计划 7 补上。
	return root
}

func newEnrollCmd() *cobra.Command {
	var hubURL, token, hubKey string

	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "用一次性注册 token 接入 hub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			res, err := Enroll(cmd.Context(), EnrollOptions{
				HubURL:       hubURL,
				Token:        token,
				ExpectHubKey: hubKey,
				Dir:          dir,
				RetryDelay:   2 * time.Second,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "已接入 %s\n", hubURL)
			fmt.Fprintf(out, "机器指纹    %s\n", res.Fingerprint)
			fmt.Fprintf(out, "hub 公钥指纹 %s\n", res.HubKeyFingerprint)
			fmt.Fprintf(out, "配置已写入   %s\n", filepath.Join(dir, ConfigFileName))
			return nil
		},
	}
	cmd.Flags().StringVar(&hubURL, "hub", "", "hub 地址，例如 https://orciny.example.com")
	cmd.Flags().StringVar(&token, "token", "", "一次性注册 token")
	cmd.Flags().StringVar(&hubKey, "hub-key", "", "hub 公钥指纹，用于带外校验（可选）")
	_ = cmd.MarkFlagRequired("hub")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本号",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), orciny.Version)
			return nil
		},
	}
}

// dirFromFlags 解析 --dir，未指定时用默认目录。所有子命令统一用它取目录。
func dirFromFlags(cmd *cobra.Command) string {
	if d, _ := cmd.Flags().GetString("dir"); d != "" {
		return d
	}
	return DefaultDir()
}
