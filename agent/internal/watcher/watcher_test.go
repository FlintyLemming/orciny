package watcher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type reports struct {
	mu    sync.Mutex
	calls [][]protocol.DriftItem
	fulls []bool
}

func (r *reports) fn(items []protocol.DriftItem, full bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, items)
	r.fulls = append(r.fulls, full)
	return nil
}

func (r *reports) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *reports) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		for _, i := range c {
			out = append(out, i.Path)
		}
	}
	return out
}

// 去抖：2 秒内的多次事件只触发一次扫描（编辑器保存会连发好几个事件）。
func TestDebounceCollapsesBurst(t *testing.T) {
	fx := newFixture(t)
	rep := &reports{}
	fx.rebuild(t, rep.fn)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fx.w.Run(ctx) }()

	fx.write(t, ".claude/CLAUDE.md", "改了\n")
	for range 5 {
		fx.notify(t, ".claude/CLAUDE.md")
	}
	require.Equal(t, 0, rep.count(), "去抖窗口内不该上报")

	fx.advance(t, 2*time.Second)
	require.Eventually(t, func() bool { return rep.count() == 1 },
		2*time.Second, 5*time.Millisecond, "去抖到期后应当上报一次")
	require.Equal(t, []string{".claude/CLAUDE.md"}, rep.paths())
}

// 节流：同一路径 30 秒内不重复上报（spec §8.1）。
func TestThrottleSuppressesRepeatWithin30s(t *testing.T) {
	fx := newFixture(t)
	rep := &reports{}
	fx.rebuild(t, rep.fn)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fx.w.Run(ctx) }()

	fx.write(t, ".claude/CLAUDE.md", "第一次改\n")
	fx.notify(t, ".claude/CLAUDE.md")
	fx.advance(t, 2*time.Second)
	require.Eventually(t, func() bool { return rep.count() == 1 }, 2*time.Second, 5*time.Millisecond)

	fx.write(t, ".claude/CLAUDE.md", "第二次改\n")
	fx.notify(t, ".claude/CLAUDE.md")
	fx.advance(t, 2*time.Second)
	require.Never(t, func() bool { return rep.count() > 1 },
		300*time.Millisecond, 20*time.Millisecond, "30 秒内同路径不该重复上报")

	fx.advance(t, 30*time.Second)
	fx.notify(t, ".claude/CLAUDE.md")
	fx.advance(t, 2*time.Second)
	require.Eventually(t, func() bool { return rep.count() == 2 },
		2*time.Second, 5*time.Millisecond, "过了节流窗口应当再报")
}

// 定时全量对账兜底 fsnotify 漏事件（网络文件系统、容器 bind mount）。
func TestReconcileTickerCatchesMissedEvents(t *testing.T) {
	fx := newFixture(t)
	rep := &reports{}
	fx.rebuild(t, rep.fn)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fx.w.Run(ctx) }()

	// 改文件但**不发**通知，模拟 fsnotify 漏了
	fx.write(t, ".claude/CLAUDE.md", "偷偷改的\n")
	require.Never(t, func() bool { return rep.count() > 0 },
		300*time.Millisecond, 20*time.Millisecond)

	fx.advance(t, 5*time.Minute)
	require.Eventually(t, func() bool { return rep.count() == 1 },
		2*time.Second, 5*time.Millisecond, "定时对账应当发现它")
	require.True(t, rep.fulls[0], "定时对账是全量的")
}

// 没有漂移时不发空的上报——每 5 分钟一条空消息是纯噪音。
func TestNoReportWhenClean(t *testing.T) {
	fx := newFixture(t)
	rep := &reports{}
	fx.rebuild(t, rep.fn)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fx.w.Run(ctx) }()

	fx.advance(t, 5*time.Minute)
	require.Never(t, func() bool { return rep.count() > 0 },
		300*time.Millisecond, 20*time.Millisecond)
}

// 暂停时完全不上报（orciny-agent pause）。
func TestPausedWatcherIsSilent(t *testing.T) {
	fx := newFixture(t)
	rep := &reports{}
	fx.rebuild(t, rep.fn)
	st := fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})
	st.Paused = true
	require.NoError(t, fx.w.Reload(st, manifest.Default(), fx.sec))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fx.w.Run(ctx) }()

	fx.write(t, ".claude/CLAUDE.md", "改了\n")
	fx.notify(t, ".claude/CLAUDE.md")
	fx.advance(t, 2*time.Second)
	require.Never(t, func() bool { return rep.count() > 0 },
		300*time.Millisecond, 20*time.Millisecond)
}

// ctx 取消后 Run 干净退出，不泄漏 goroutine 与 fsnotify 句柄。
func TestRunStopsOnContextCancel(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- fx.w.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未在 ctx 取消后退出")
	}
}
