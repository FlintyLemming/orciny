package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/agent/internal/watcher"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// LocalDrift 在本机做一次全量对账，不联网、不起监视。
//
// 没有 state.json 时返回空切片（还没 apply 过，谈不上漂移）。
func LocalDrift(dir string) ([]protocol.DriftItem, error) {
	st, err := state.Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	home, err := cfg.ManagedHomeDir()
	if err != nil {
		return nil, err
	}
	sec, err := secrets.Load(dir)
	if err != nil {
		return nil, err
	}
	m, err := manifestOfState(st)
	if err != nil {
		return nil, err
	}

	w, err := watcher.New(watcher.Options{
		Dir:         dir,
		ManagedHome: home,
		Clock:       clock.System(),
		Report:      nil, // 不联网
	})
	if err != nil {
		return nil, err
	}
	if err := w.Reload(st, m, sec); err != nil {
		return nil, err
	}
	return w.Scan(true)
}

// SetPaused 写 state.json 的 paused 标志。
//
// 从没 apply 过时建一份最小 state，而不是报错——pause 是本地开关，
// 落盘即生效；上报 hub 是附加的（由下一次握手的 MachineInfo.LocalPaused 带上）。
func SetPaused(dir string, paused bool) error {
	st, err := state.Load(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		st = &state.State{
			Files:  map[string]state.FileState{},
			Health: state.HealthOK,
		}
	}
	st.Paused = paused
	return state.Save(dir, st)
}

func newDriftCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drift",
		Short: "查看本机相对基线的漂移（离线，含简易 diff）",
		Long: `在本机做一次全量对账并打印差异，不联网。

这份 diff 由本机计算，仅供排查；收编与恢复请在 Web 上操作。
打印前会把凭据值替回占位符，终端输出不含明文密钥。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			if _, err := loadConfigForCmd(dir); err != nil {
				return err
			}
			items, err := LocalDrift(dir)
			if err != nil {
				return err
			}
			return printDrift(cmd.OutOrStdout(), dir, items)
		},
	}
}

func printDrift(out io.Writer, dir string, items []protocol.DriftItem) error {
	if len(items) == 0 {
		fmt.Fprintln(out, "本机无漂移。")
		return nil
	}
	fmt.Fprintf(out, "本机漂移（%d 项）：\n\n", len(items))

	st, _ := state.Load(dir)
	sec, _ := secrets.Load(dir)
	if sec == nil {
		sec = &secrets.File{}
	}
	cache := blobcache.New(dir)
	cfg, _ := LoadConfig(dir)
	home := ""
	if cfg != nil {
		home, _ = cfg.ManagedHomeDir()
	}

	for _, it := range items {
		fmt.Fprintf(out, "  %-9s %s\n", kindName(it.Kind), it.Path)
		if it.Truncated {
			fmt.Fprintln(out, "    （内容已截断或不安全，只报路径）")
			fmt.Fprintln(out)
			continue
		}

		// 基线：blob 的占位符形态；当前：item.Content（watcher 已还原）
		// 或现读磁盘再还原一次（item 没带内容时）。
		base := baselineContent(cache, st, it.Path)
		cur := it.Content
		if len(cur) == 0 && home != "" && it.Kind != protocol.DriftDeleted {
			if raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(it.Path))); err == nil {
				res := render.Restore(raw, render.Values{
					Creds: sec.Creds, Vars: sec.Vars, Provider: sec.Provider,
				})
				if res.Safe {
					cur = res.Content
				}
			}
		}
		// 基线也要保证是占位符形态（blob 本身就是）。
		for _, line := range lineDiff(string(base), string(cur)) {
			fmt.Fprintf(out, "    %s\n", line)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "提示：这份差异由本机计算，仅供排查；收编与恢复请在 Web 上操作。")
	return nil
}

func baselineContent(cache *blobcache.Cache, st *state.State, rel string) []byte {
	if st == nil {
		return nil
	}
	fs, ok := st.Files[rel]
	if !ok || fs.Blob == "" {
		return nil
	}
	b, err := cache.Get(fs.Blob)
	if err != nil {
		return nil
	}
	return b
}

func kindName(k uint8) string {
	switch k {
	case protocol.DriftAdded:
		return "added"
	case protocol.DriftModified:
		return "modified"
	case protocol.DriftDeleted:
		return "deleted"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}

func manifestOfState(st *state.State) (manifest.Manifest, error) {
	if len(st.Manifest) == 0 {
		return manifest.Default(), nil
	}
	return manifest.Parse(st.Manifest)
}

// lineDiff 是一个极简的行级 LCS diff，专供终端排查。
//
// agent 不引 go-difflib（那是 hub 侧依赖，agent 要瘦）。输出形如：
//
//   - 旧行
//   - 新行
//
// 公共行不打印，避免把整个文件刷到屏幕上。
func lineDiff(a, b string) []string {
	as := splitLines(a)
	bs := splitLines(b)
	if len(as) == 0 && len(bs) == 0 {
		return nil
	}
	// LCS 表
	n, m := len(as), len(bs)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if as[i] == bs[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		if as[i] == bs[j] {
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			out = append(out, "- "+as[i])
			i++
		} else {
			out = append(out, "+ "+bs[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "- "+as[i])
	}
	for ; j < m; j++ {
		out = append(out, "+ "+bs[j])
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	// 保留末行空串语义：strings.Split 对 "a\n" 会给出 ["a", ""]，
	// 对排查来说末尾空行噪音大，去掉。
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
