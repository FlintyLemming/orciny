# 子计划 12 · watcher：fsnotify、去抖、定时对账、节流

**前置**：05、06、07（读 `state`）、11
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`agent/internal/watcher` —— manifest 展开后的目录监视、2 秒去抖、5 分钟定时全量对账、同路径 30 秒上报节流、`tree` 模式识别新增文件。

**检测规则（spec §8.1）**

- **fsnotify** 监视 manifest 展开后的目录集合。`.claude.json` 在 HOME 根下，监视 HOME 目录但**只对这一个文件名响应**（噪音在 agent 内部过滤，不产生 IO）。
- **去抖 2 秒**：编辑器保存往往触发多个事件。
- **定时全量对账**默认 5 分钟。兜底 fsnotify 漏事件：网络文件系统、容器 bind mount、inotify 队列溢出。
- **上报节流**：同一路径 30 秒内不重复上报。

**测试纪律**：全部时长走注入的 `clock.Clock`，**不允许 `time.Sleep`**——真睡的话这一个包就要跑几分钟。

---

### Task 1: 引入 fsnotify 为直接依赖

**Files:**
- Modify: `go.mod`

`fsnotify v1.10.1` 已作为 PocketBase 的间接依赖在 `go.sum` 里。

- [ ] **Step 1: 转直接依赖**

```bash
go get github.com/fsnotify/fsnotify@v1.10.1
go mod tidy
head -3 go.mod   # 确认 go 指令仍是 go 1.26.0
```

- [ ] **Step 2: 提交**

```bash
git add go.mod go.sum
git commit -m "chore: fsnotify 转为直接依赖"
```

---

### Task 2: 对账扫描

**Files:**
- Create: `agent/internal/watcher/scan.go`
- Test: `agent/internal/watcher/scan_test.go`

**Interfaces:**
- Consumes: `manifest.Manifest.Expand`、`state.State`、`render.Restore`、`blobcache`
- Produces:
  ```go
  type Options struct {
      Dir         string
      ManagedHome string
      Clock       clock.Clock
      Debounce    time.Duration // 0 → 2s
      Reconcile   time.Duration // 0 → 5m
      Throttle    time.Duration // 0 → 30s
      Report      func(items []protocol.DriftItem, full bool) error
      Logger      *slog.Logger
  }
  type Watcher struct{ /* ... */ }
  func New(o Options) (*Watcher, error)
  func (w *Watcher) Reload(st *state.State, m manifest.Manifest, sec *secrets.File) error
  func (w *Watcher) Scan(full bool) ([]protocol.DriftItem, error)
  ```

**比对口径（spec §6.3）**：只比 `rendered` hash。`keys` 模式只比受管键子树的**规范化 JSON**（键排序、无空白）hash——用户或 Claude Code 改动其他键完全不算漂移，这正是 `keys` 模式存在的理由（spec §7.5）。

**三种漂移**：`added`（磁盘上有、基线里没有，只有 `tree` 模式会产生）、`modified`、`deleted`。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/watcher/scan_test.go`：

```go
package watcher_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
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
	// 一个长度为 1 的凭据值几乎必然还原不干净
	fx.sec.Creds["bad"] = "x"
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})
	fx.write(t, ".claude/CLAUDE.md", "文中有 x 这个字符\n")

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
```

`renderRestore` 是测试里的小包装，放在 `scan_test.go` 顶部：

```go
func renderRestore(disk string, sec *secrets.File) render.RestoreResult {
	return render.Restore([]byte(disk), sec.Creds, sec.Vars)
}
```

`TestCanonicalKeysHashIgnoresFormatting` 用到 `encoding/json` 之外的东西都在 `watcher` 包里，测试文件只需 import `encoding/json`（若最终没用到就删掉该 import）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/watcher/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `agent/internal/watcher/scan.go`。要点：

```go
// CanonicalKeysHash 是 keys 模式的比对口径（spec §7.5）：
// 只取受管键子树，规范化（键排序、无空白）后取 hash。
//
// 用户或 Claude Code 改动其他键完全不算漂移——这正是 keys 模式存在的理由。
func CanonicalKeysHash(content []byte, keys []string) string {
	sub := map[string]any{}
	for _, k := range keys {
		v := gjson.GetBytes(content, k)
		if !v.Exists() {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(v.Raw), &decoded); err != nil {
			continue
		}
		sub[k] = decoded
	}
	// encoding/json 的 map 序列化按键排序，正是要的规范化。
	b, err := json.Marshal(sub)
	if err != nil {
		return ""
	}
	return blobcache.Hash(b)
}
```

`Scan` 的骨架：

1. `w.manifest.Expand(w.o.ManagedHome)` 拿到磁盘现状。
2. 遍历展开结果：跳过忽略清单命中的；读内容；超限则 `Truncated`；算比对 hash（`keys` 模式走 `CanonicalKeysHash`，否则 `blobcache.Hash`）。
3. 与 `state.Files[rel].Rendered` 比：不在基线里 → `added`；不同 → `modified`；相同 → 无。
4. 遍历 `state.Files` 里磁盘上已不存在的 → `deleted`。
5. 对 `added` / `modified` 调 `render.RestoreWithBase`（基线内容从 `blobcache.Get(fs.Blob)` 取）；`Safe == false` → 清空 `Content` 并置 `Truncated`；`Partial` → 置 `RestorePartial`。
6. 按 `Path` 排序返回。

`Reload` 只是把 `st` / `manifest` / `secrets` 换掉（加锁），并重算要监视的目录集合。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/watcher/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/internal/watcher/
git commit -m "feat: 漂移对账扫描"
```

---

### Task 3: fsnotify、去抖、定时对账与节流

**Files:**
- Create: `agent/internal/watcher/watcher.go`
- Test: `agent/internal/watcher/watcher_test.go`

**Interfaces:**
- Produces: `func (w *Watcher) Run(ctx context.Context) error`、`func (w *Watcher) Notify(path string)`（测试注入事件用，生产由 fsnotify 调）

**设计**

- `Run` 起两条腿：fsnotify 事件流与对账 ticker，都在同一个 `select` 里，一条 goroutine。
- 去抖：收到事件后重置一个 2 秒的定时器；定时器到期才扫。
- 节流：`lastReported map[string]time.Time`，同路径 30 秒内不重复上报。
- **`.claude.json` 特判**：监视 HOME 目录，但只对这一个文件名响应，其余事件在内存里丢掉，不产生 IO（spec §8.1）。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/watcher/watcher_test.go`：

```go
package watcher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/watcher"
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
	fx.rebuild(t, rep.fn) // 用 rep 重建 watcher，其余不变
	fx.baseline(t, map[string]string{".claude/CLAUDE.md": "基线\n"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fx.w.Run(ctx) }()

	fx.write(t, ".claude/CLAUDE.md", "改了\n")
	for range 5 {
		fx.w.Notify(".claude/CLAUDE.md")
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
	fx.w.Notify(".claude/CLAUDE.md")
	fx.advance(t, 2*time.Second)
	require.Eventually(t, func() bool { return rep.count() == 1 }, 2*time.Second, 5*time.Millisecond)

	fx.write(t, ".claude/CLAUDE.md", "第二次改\n")
	fx.w.Notify(".claude/CLAUDE.md")
	fx.advance(t, 2*time.Second)
	require.Never(t, func() bool { return rep.count() > 1 },
		300*time.Millisecond, 20*time.Millisecond, "30 秒内同路径不该重复上报")

	fx.advance(t, 30*time.Second)
	fx.w.Notify(".claude/CLAUDE.md")
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
	fx.w.Notify(".claude/CLAUDE.md")
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
```

`fixture` 需要两个新方法（加进 `scan_test.go`）：

```go
// rebuild 用给定的上报函数重建 watcher，其余配置不变。
func (fx *fixture) rebuild(t *testing.T, report func([]protocol.DriftItem, bool) error) {
	t.Helper()
	w, err := watcher.New(watcher.Options{
		Dir: fx.dir, ManagedHome: fx.home, Clock: fx.clk, Report: report,
	})
	require.NoError(t, err)
	fx.w = w
}

// advance 推进假时钟。定时器挂在 Run 那个 goroutine 上，一次性推进可能
// 赶在它前面，因此按 M0 教训 L 的做法：轮询里反复推进直到定时器已挂上。
func (fx *fixture) advance(t *testing.T, d time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		return fx.clk.TimerCount() > 0
	}, 2*time.Second, 5*time.Millisecond, "定时器未挂上")
	fx.clk.Advance(d)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/watcher/ -run 'Debounce|Throttle|Reconcile|Paused|RunStops|NoReport' -v`
Expected: FAIL，`w.Run undefined`

- [ ] **Step 3: 实现**

Create `agent/internal/watcher/watcher.go`。骨架：

```go
// Run 是 watcher 的主循环：fsnotify 事件与对账 ticker 在同一个 select 里，
// 一条 goroutine 管到底。
//
// 三个时长全部走注入的时钟：去抖 2 秒、对账 5 分钟、节流 30 秒。真睡的话
// 这个包的测试要跑几分钟（spec §10.1）。
func (w *Watcher) Run(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: 建立文件监视: %w", err)
	}
	defer fsw.Close()

	if err := w.addWatches(fsw); err != nil {
		// 监视建不起来不是致命的：定时对账仍然兜得住，
		// 只是发现漂移会慢到 5 分钟以内。
		w.log.Warn("建立文件监视失败，退化为仅定时对账", "error", err)
	}

	ticker := w.clk.NewTicker(w.reconcile)
	defer ticker.Stop()

	var debounce clock.Timer
	var debounceC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return nil

		case ev, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if !w.interested(ev.Name) {
				continue // 噪音在内存里丢掉，不产生 IO
			}
			// 去抖：编辑器保存往往触发多个事件（spec §8.1）
			if debounce != nil {
				debounce.Stop()
			}
			debounce = w.clk.NewTimer(w.debounce)
			debounceC = debounce.C()

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			w.log.Warn("文件监视报错", "error", err)

		case <-debounceC:
			debounceC = nil
			w.reportScan(false)

		case <-ticker.C():
			// 定时全量对账兜底 fsnotify 漏事件：网络文件系统、
			// 容器 bind mount、inotify 队列溢出（spec §8.1）
			w.reportScan(true)

		case <-w.manual:
			// Notify 注入的事件（测试与 CLI 的 drift 子命令）
			if debounce != nil {
				debounce.Stop()
			}
			debounce = w.clk.NewTimer(w.debounce)
			debounceC = debounce.C()
		}
	}
}
```

`interested` 处理 `.claude.json` 特判：

```go
// interested 报告某个路径是否值得触发一次扫描。
//
// .claude.json 在 HOME 根下，因此必须监视整个 HOME 目录——但那里什么都有。
// 只对这一个文件名响应，其余事件在内存里丢掉，不产生任何 IO（spec §8.1）。
func (w *Watcher) interested(abs string) bool {
	rel, err := filepath.Rel(w.o.ManagedHome, abs)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if !strings.Contains(rel, "/") {
		// HOME 根下：只认 .claude.json
		return rel == ".claude.json"
	}
	if manifest.IsAlwaysExcluded(rel) {
		return false
	}
	return true
}
```

`reportScan`：

```go
func (w *Watcher) reportScan(full bool) {
	w.mu.Lock()
	paused := w.st != nil && w.st.Paused
	w.mu.Unlock()
	if paused {
		return
	}

	items, err := w.Scan(full)
	if err != nil {
		w.log.Warn("对账失败", "error", err)
		return
	}
	items = w.throttle(items, full)
	if len(items) == 0 {
		return // 没漂移就不发空消息：每 5 分钟一条纯噪音
	}
	if err := w.o.Report(items, full); err != nil {
		w.log.Warn("上报漂移失败", "error", err)
		return
	}
	w.markReported(items)
}

// throttle 滤掉 30 秒内已上报过的路径。
//
// 全量对账（full）不受节流影响：它是兜底路径，且 hub 侧靠部分唯一索引
// 做去重——同一路径重复检测到就更新那条，不新建（spec §4.1）。
func (w *Watcher) throttle(items []protocol.DriftItem, full bool) []protocol.DriftItem {
	if full {
		return items
	}
	now := w.clk.Now()
	out := items[:0]
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, it := range items {
		if last, ok := w.lastReported[it.Path]; ok && now.Sub(last) < w.throttleWindow {
			continue
		}
		out = append(out, it)
	}
	return out
}
```

`Notify` 是一个非阻塞写入 `w.manual` 的方法，供测试与 CLI 使用。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/watcher/... -race`
Expected: PASS，无数据竞争

- [ ] **Step 5: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add agent/internal/watcher/
git commit -m "feat: fsnotify 监视、去抖、定时对账与上报节流"
```
