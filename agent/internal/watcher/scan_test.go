package watcher_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/agent/internal/watcher"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type fixture struct {
	dir  string
	home string
	clk  *clock.Fake
	w    *watcher.Watcher
	sec  *secrets.File
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{
		dir:  t.TempDir(),
		home: t.TempDir(),
		clk:  clock.NewFake(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)),
		sec: &secrets.File{
			Creds:   map[string]string{"k": "sk-real-value-1234"},
			Vars:    map[string]string{"ws": "workspace-main"},
			Machine: map[string]string{},
		},
	}
	w, err := watcher.New(watcher.Options{
		Dir: fx.dir, ManagedHome: fx.home, Clock: fx.clk,
		Report: func([]protocol.DriftItem, bool) error { return nil },
	})
	require.NoError(t, err)
	fx.w = w
	return fx
}

func (fx *fixture) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(fx.home, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// baseline 造一份「磁盘内容就是基线」的 state。
func (fx *fixture) baseline(t *testing.T, files map[string]string) *state.State {
	t.Helper()
	st := &state.State{Files: map[string]state.FileState{}, Health: state.HealthOK}
	cache := blobcache.New(fx.dir)
	for rel, disk := range files {
		fx.write(t, rel, disk)
		res := render0(t, disk, fx.sec)
		blob, err := cache.Put(res)
		require.NoError(t, err)
		st.Files[rel] = state.FileState{
			Blob: blob, Rendered: blobcache.Hash([]byte(disk)),
			Mode: 0o644, Size: uint32(len(disk)),
		}
	}
	require.NoError(t, fx.w.Reload(st, manifest.Default(), fx.sec))
	return st
}

// render0 把磁盘内容还原成占位符形态，当作 blob 内容存起来。
func render0(t *testing.T, disk string, sec *secrets.File) []byte {
	t.Helper()
	res := renderRestore(disk, sec)
	require.True(t, res.Safe)
	return res.Content
}

func renderRestore(disk string, sec *secrets.File) render.RestoreResult {
	return render.Restore([]byte(disk), sec.Creds, sec.Vars)
}

// rebuild 用给定的上报函数重建 watcher，其余配置不变。
func (fx *fixture) rebuild(t *testing.T, report func([]protocol.DriftItem, bool) error) {
	t.Helper()
	w, err := watcher.New(watcher.Options{
		Dir: fx.dir, ManagedHome: fx.home, Clock: fx.clk, Report: report,
	})
	require.NoError(t, err)
	fx.w = w
}

// notify 注入事件并等到 debounce 定时器挂上。
//
// Run 启动后 reconcile ticker 一直在册，TimerCount 恒 ≥ 1；
// 若只等「有定时器」再 Advance，会赶在 debounce 臂上之前把时钟推走，
// 事件就丢了（M0 教训 L 的变体）。
func (fx *fixture) notify(t *testing.T, path string) {
	t.Helper()
	before := fx.clk.TimerCount()
	fx.w.Notify(path)
	require.Eventually(t, func() bool {
		n := fx.clk.TimerCount()
		// 新挂 debounce：1→2；或替换仍在册的 debounce：保持 ≥ 2
		return n > before || n >= 2
	}, 2*time.Second, 5*time.Millisecond, "debounce 定时器未挂上")
}

// advance 推进假时钟。调用前须已有在册定时器（ticker 或 debounce）。
func (fx *fixture) advance(t *testing.T, d time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		return fx.clk.TimerCount() > 0
	}, 2*time.Second, 5*time.Millisecond, "定时器未挂上")
	fx.clk.Advance(d)
}

func TestScanCleanStateHasNoDrift(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{
		".claude/CLAUDE.md":     "# 规矩\n",
		".claude/settings.json": `{"K":"sk-real-value-1234"}`,
	})
	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Empty(t, items, "内容与基线一致时不该有漂移")
}

func TestScanDetectsModified(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "原始\n"})
	fx.write(t, ".claude/CLAUDE.md", "改过了\n")

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, ".claude/CLAUDE.md", items[0].Path)
	require.Equal(t, protocol.DriftModified, items[0].Kind)
	require.Equal(t, "改过了\n", string(items[0].Content))
	require.NotEmpty(t, items[0].BaseHash)
}

// tree 模式含新增文件——这是核心用例的支点（spec §3.1）。
func TestScanDetectsAddedInTreeMode(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{".claude/skills/foo/SKILL.md": "旧的\n"})
	fx.write(t, ".claude/skills/bar/SKILL.md", "新写的技能\n")

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, ".claude/skills/bar/SKILL.md", items[0].Path)
	require.Equal(t, protocol.DriftAdded, items[0].Kind)
	require.Empty(t, items[0].BaseHash, "added 没有基线 hash")
	require.Equal(t, "新写的技能\n", string(items[0].Content))
}

func TestScanDetectsDeleted(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "内容\n"})
	require.NoError(t, os.Remove(filepath.Join(fx.home, ".claude/CLAUDE.md")))

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, protocol.DriftDeleted, items[0].Kind)
	require.Empty(t, items[0].Content, "deleted 不带内容")
	require.NotEmpty(t, items[0].BaseHash)
}

// 上报的内容必须已经还原成占位符——绝不能把明文密钥交给 hub（spec §6.4）。
func TestScanRestoresCredentialsBeforeReporting(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{
		".claude/settings.json": `{"K":"sk-real-value-1234"}`,
	})
	fx.write(t, ".claude/settings.json", `{"K":"sk-real-value-1234","new":"x"}`)

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotContains(t, string(items[0].Content), "sk-real-value-1234")
	require.Contains(t, string(items[0].Content), "{{cred.k}}")
	require.False(t, items[0].Truncated)
}

// 还原不干净 → 只报路径，不带内容（spec §6.4）。
func TestScanTruncatesWhenRestoreIsUnsafe(t *testing.T) {
	fx := newFixture(t)
	// 凭据值落在 "{" 后面时，替换后变成 "{{{cred.k}}"，Parse 挂掉，
	// roundtrip 失败 → Safe=false（restore.go 注释里的典型反例）。
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})
	fx.write(t, ".claude/CLAUDE.md", "{sk-real-value-1234\n")

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Truncated)
	require.Empty(t, items[0].Content, "不安全时绝不带内容")
}

// 变量还原失败时标记但照常上报。
func TestScanMarksRestorePartial(t *testing.T) {
	fx := newFixture(t)
	fx.sec.Vars["tier"] = "1" // 太短，会被放弃还原
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})
	fx.write(t, ".claude/CLAUDE.md", "现在是 tier 1 了\n")

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].RestorePartial)
	require.NotEmpty(t, items[0].Content, "变量还原失败不影响上报内容")
}

// keys 模式：只比受管键子树，改别的键不算漂移（spec §7.5）。
func TestScanKeysModeIgnoresUnmanagedKeys(t *testing.T) {
	fx := newFixture(t)
	disk := `{"numStartups":1,"mcpServers":{"a":{}},"projects":{}}`
	fx.write(t, ".claude.json", disk)

	st := &state.State{Files: map[string]state.FileState{
		".claude.json": {
			Blob: "b", Rendered: watcher.CanonicalKeysHash([]byte(disk), []string{"mcpServers"}),
			Mode: 0o644, Keys: []string{"mcpServers"},
		},
	}, Health: state.HealthOK}
	require.NoError(t, fx.w.Reload(st, manifest.Default(), fx.sec))

	// 改非受管键：不算漂移
	fx.write(t, ".claude.json", `{"numStartups":99,"mcpServers":{"a":{}},"projects":{"x":1}}`)
	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Empty(t, items)

	// 改受管键：算
	fx.write(t, ".claude.json", `{"numStartups":99,"mcpServers":{"b":{}}}`)
	items, err = fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, ".claude.json", items[0].Path)
}

// 受管键子树的比对必须与键顺序、空白无关。
func TestCanonicalKeysHashIgnoresFormatting(t *testing.T) {
	a := watcher.CanonicalKeysHash([]byte(`{"mcpServers":{"a":{"x":1,"y":2}}}`), []string{"mcpServers"})
	b := watcher.CanonicalKeysHash(
		[]byte("{\n  \"mcpServers\" : {\n    \"a\" : { \"y\":2, \"x\":1 }\n  }\n}"),
		[]string{"mcpServers"})
	require.Equal(t, a, b)
}

// 忽略清单里的路径不产生漂移（spec §8.4）。
func TestScanRespectsIgnoreList(t *testing.T) {
	fx := newFixture(t)
	st := fx.baseline(t, map[string]string{".claude/skills/foo/SKILL.md": "基线\n"})
	st.Ignored = []string{".claude/skills/**"}
	require.NoError(t, fx.w.Reload(st, manifest.Default(), fx.sec))

	fx.write(t, ".claude/skills/foo/SKILL.md", "改了\n")
	fx.write(t, ".claude/skills/new/SKILL.md", "新的\n")
	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Empty(t, items)
}

// 恒排除的东西永远不产生漂移，也就永远不会被上报。
func TestScanNeverReportsAlwaysExcluded(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})
	fx.write(t, ".claude/projects/a/session.jsonl", "会话历史")
	fx.write(t, ".claude/.credentials.json", `{"oauth":"绝密"}`)

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Empty(t, items)
}

// 超限文件只报路径并置 Truncated（spec §5.4）。
func TestScanTruncatesOversizeFile(t *testing.T) {
	fx := newFixture(t)
	fx.baseline(t, map[string]string{".claude/skills/foo/SKILL.md": "基线\n"})
	big := make([]byte, protocol.MaxFileSize+1)
	for i := range big {
		big[i] = 'x'
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(fx.home, ".claude/skills/foo/HUGE.md"), big, 0o644))

	items, err := fx.w.Scan(true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Truncated)
	require.Empty(t, items[0].Content)
}
