package agent

import (
	"fmt"

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
	// enroll / run / status 由计划 4 与计划 7 补上。
	return root
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
