package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/logging"
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
	root.AddCommand(newRunCmd(), newStatusCmd(), newSyncCmd())
	return root
}

func newSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "立即从 hub 拉取并应用当前配置",
		Long: `立即从 hub 拉取当前指派的配置集并 apply，打印计划与结果后退出。

本命令会短暂中断常驻 agent 的连接，它会在几秒内自动重连。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			if _, err := loadConfigForCmd(dir); err != nil {
				return err
			}
			rep, err := SyncOnce(cmd.Context(), SyncOptions{Dir: dir, Logger: slog.Default()})
			if rep != nil {
				fmt.Fprint(cmd.OutOrStdout(), formatSyncReport(rep))
			}
			return err
		},
	}
}

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "前台运行 agent（服务单元调用的就是它）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)

			cfg, err := loadConfigForCmd(dir)
			if err != nil {
				return err
			}

			out := io.Writer(os.Stdout)
			if cfg.LogFile {
				fw, err := logging.FileWriter(filepath.Join(dir, "logs"), 14)
				if err != nil {
					return err
				}
				defer fw.Close()
				out = io.MultiWriter(os.Stdout, fw)
			}
			log := logging.New(out, slog.LevelInfo)

			// systemd 的 SIGTERM 与 Ctrl-C 都要能干净退出。
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			err = Run(ctx, RunOptions{Dir: dir, Logger: log})
			if errors.Is(err, context.Canceled) {
				log.Info("agent 已停止")
				return nil
			}
			return err
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看连接状态、配置版本、健康与漂移",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			out := cmd.OutOrStdout()

			cfg, err := loadConfigForCmd(dir)
			if err != nil {
				return err
			}
			id, err := identity.Load(filepath.Join(dir, identity.DirName))
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "hub 地址   %s\n", cfg.HubURL)
			fmt.Fprintf(out, "机器指纹   %s\n", id.Fingerprint())
			fmt.Fprintf(out, "agent 版本 %s\n", orciny.Version)

			if home, err := cfg.ManagedHomeDir(); err == nil {
				fmt.Fprintf(out, "受管 HOME  %s\n", home)
			}

			st, err := LoadStatus(dir)
			switch {
			case errors.Is(err, os.ErrNotExist):
				fmt.Fprintf(out, "连接状态   未运行（没有找到 %s）\n", StatusFileName)
			case err != nil:
				return err
			default:
				fmt.Fprintf(out, "连接状态   %s（自 %s）\n",
					st.State, st.Since.Local().Format(time.RFC3339))
				if st.LastError != "" {
					fmt.Fprintf(out, "最近错误   %s\n", st.LastError)
				}
			}

			printConfigStatus(out, dir)
			return nil
		},
	}
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

// loadConfigForCmd 读配置，并把「文件不存在」翻译成人话。
// 没 enroll 就 run/status 是最常见的首次使用错误，不该甩一行 ENOENT 出去。
func loadConfigForCmd(dir string) (*Config, error) {
	cfg, err := LoadConfig(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("尚未 enroll：%s 下没有 %s，请先执行 orciny-agent enroll --hub ... --token ...",
			dir, ConfigFileName)
	}
	return cfg, err
}

// dirFromFlags 解析 --dir，未指定时用默认目录。所有子命令统一用它取目录。
func dirFromFlags(cmd *cobra.Command) string {
	if d, _ := cmd.Flags().GetString("dir"); d != "" {
		return d
	}
	return DefaultDir()
}
