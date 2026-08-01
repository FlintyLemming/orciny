package agent

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "暂停本机配置管理（不再 apply / 上报漂移）",
		Long: `把本机标记为暂停：常驻 agent 不再 apply 下发、不再上报漂移。

这是本地开关，落盘即生效；即便 hub 离线也能改。面板会在下次握手时
通过 MachineInfo.LocalPaused 得知。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			if err := SetPaused(dir, true); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "已暂停本机配置管理。恢复：orciny-agent resume")
			return nil
		},
	}
}

func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "恢复本机配置管理",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			if err := SetPaused(dir, false); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "已恢复本机配置管理。")
			return nil
		},
	}
}
