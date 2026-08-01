# 子计划 07 · applier：plan、快照、原子落盘、回滚、keys 合并

**前置**：05（`internal/manifest`）、06（`state` / `secrets` / `blobcache` / `render`）
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`agent/internal/applier` —— 五种动作的 plan、apply 前快照、原子落盘、失败逆序回滚、`degraded` 终态、`~/.claude.json` 的 `keys` 合并、`state.json` 更新。

**为什么这是 M1 最要紧的一块（spec §1.4 / §7.4）**：这是 Orciny **唯一会写用户文件**的地方。「宁可不动，不可写坏」如果没在第一版就成立，用户会在被写坏一次之后永久卸载。

**新增依赖**：`github.com/tidwall/sjson` 与 `github.com/tidwall/gjson`。

---

### Task 1: 引入 sjson 依赖

**Files:**
- Modify: `go.mod`、`go.sum`

- [ ] **Step 1: 装依赖**

```bash
go get github.com/tidwall/sjson@latest
go get github.com/tidwall/gjson@latest
go mod tidy
```

- [ ] **Step 2: 确认 go 指令没被改动**

Run: `head -3 go.mod`
Expected: `go 1.26.0`（`go get` 有时会把它改成工具链版本，若变了改回来）

- [ ] **Step 3: 提交**

```bash
git add go.mod go.sum
git commit -m "chore: 引入 tidwall/sjson 用于 keys 模式的原地 JSON 编辑"
```

---

### Task 2: plan 的五种动作

**Files:**
- Create: `agent/internal/applier/plan.go`
- Test: `agent/internal/applier/plan_test.go`

**Interfaces:**
- Consumes: `protocol.ConfigSnapshot` / `protocol.FileEntry` / `protocol.Action*`、`state.State`、`render.Render`
- Produces:
  ```go
  type Step struct {
      Rel      string
      Action   uint8
      Blob     string
      Rendered string
      Mode     os.FileMode
      Keys     []string
      Content  []byte // 已渲染；skip / delete 时为 nil
  }
  type Plan struct {
      Steps []Step
      Skip  []manifest.Skip
  }
  func BuildPlan(snap protocol.ConfigSnapshot, st *state.State, content map[string][]byte, look func(protocol.Ref) (string, bool)) (Plan, error)
  func (p Plan) Writes() int
  ```

**五种动作（spec §7.4）**

| 动作 | 条件 |
|---|---|
| `skip` | 磁盘 rendered hash == 期望 hash |
| `create` | 期望存在，磁盘不存在 |
| `overwrite` | 都存在但 hash 不同 |
| `delete` | 上一版有、本版无，且磁盘存在 |
| `merge` | `keys` 模式 |

**权限位**：含凭据引用的文件 0600，其余 0644——由 `FileEntry.Mode` 携带，plan 直接采用；`Mode` 为 0 时按「含 `cred.` 引用则 0600，否则 0644」推导。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/applier/plan_test.go`：

```go
package applier_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/protocol"
)

func sec() *secrets.File {
	return &secrets.File{
		Creds:   map[string]string{"k": "sk-real-value"},
		Vars:    map[string]string{"ws": "main"},
		Machine: map[string]string{"hostname": "mac", "os": "darwin", "arch": "arm64", "name": "主力"},
	}
}

// snapFor 造一份只含这些文件的快照，内容取自 content。
func snapFor(content map[string][]byte, modes map[string]uint32) (protocol.ConfigSnapshot, map[string][]byte) {
	snap := protocol.ConfigSnapshot{ConfigSetID: "set", RevisionID: "rev", Seq: 1}
	byHash := map[string][]byte{}
	for rel, c := range content {
		h := blobcache.Hash(c)
		mode := modes[rel]
		if mode == 0 {
			mode = 0o644
		}
		snap.Files = append(snap.Files, protocol.FileEntry{
			Path: rel, Hash: h, Size: uint32(len(c)), Mode: mode,
		})
		byHash[h] = c
	}
	snap.Checksum = protocol.Checksum(snap.Files)
	return snap, byHash
}

func stepFor(t *testing.T, p applier.Plan, rel string) applier.Step {
	t.Helper()
	for _, s := range p.Steps {
		if s.Rel == rel {
			return s
		}
	}
	t.Fatalf("plan 里没有 %s", rel)
	return applier.Step{}
}

func TestPlanCreateWhenAbsent(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("# 规矩")}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Len(t, p.Steps, 1)
	require.Equal(t, protocol.ActionCreate, p.Steps[0].Action)
	require.Equal(t, []byte("# 规矩"), p.Steps[0].Content)
}

func TestPlanSkipWhenRenderedHashMatches(t *testing.T) {
	content := []byte(`{"K":"{{cred.k}}"}`)
	snap, blobs := snapFor(map[string][]byte{".claude/settings.json": content},
		map[string]uint32{".claude/settings.json": 0o600})

	rendered := []byte(`{"K":"sk-real-value"}`)
	st := &state.State{Files: map[string]state.FileState{
		".claude/settings.json": {
			Blob:     blobcache.Hash(content),
			Rendered: blobcache.Hash(rendered),
			Mode:     0o600,
		},
	}}

	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, protocol.ActionSkip, stepFor(t, p, ".claude/settings.json").Action)
	require.Equal(t, 0, p.Writes(), "全 skip 时零写入")
}

// 凭据轮换：blob 没变，但渲染后的内容变了 → 必须 overwrite（spec §5.1）。
func TestPlanOverwriteAfterRotation(t *testing.T) {
	content := []byte(`{"K":"{{cred.k}}"}`)
	snap, blobs := snapFor(map[string][]byte{".claude/settings.json": content},
		map[string]uint32{".claude/settings.json": 0o600})

	old := []byte(`{"K":"sk-OLD-value"}`)
	st := &state.State{Files: map[string]state.FileState{
		".claude/settings.json": {
			Blob:     blobcache.Hash(content),
			Rendered: blobcache.Hash(old),
			Mode:     0o600,
		},
	}}

	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, protocol.ActionOverwrite, stepFor(t, p, ".claude/settings.json").Action)
	require.Equal(t, []byte(`{"K":"sk-real-value"}`), stepFor(t, p, ".claude/settings.json").Content)
}

// 上一版有、本版无 → delete。
func TestPlanDeleteWhenDroppedFromRevision(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("keep")}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/CLAUDE.md": {Blob: "x", Rendered: "y", Mode: 0o644},
		".claude/gone.md":   {Blob: "a", Rendered: "b", Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, protocol.ActionDelete, stepFor(t, p, ".claude/gone.md").Action)
	require.Nil(t, stepFor(t, p, ".claude/gone.md").Content)
}

func TestPlanMergeForKeysMode(t *testing.T) {
	content := []byte(`{"mcpServers":{"a":{}}}`)
	h := blobcache.Hash(content)
	snap := protocol.ConfigSnapshot{
		Files: []protocol.FileEntry{{
			Path: ".claude.json", Hash: h, Size: uint32(len(content)),
			Mode: 0o644, Keys: []string{"mcpServers"},
		}},
	}
	p, err := applier.BuildPlan(snap, nil, map[string][]byte{h: content}, sec().Lookup)
	require.NoError(t, err)
	s := stepFor(t, p, ".claude.json")
	require.Equal(t, protocol.ActionMerge, s.Action, "keys 模式永远是 merge")
	require.Equal(t, []string{"mcpServers"}, s.Keys)
}

// 忽略清单里的路径不进 plan（spec §8.4）。
func TestPlanRespectsIgnorePaths(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":              []byte("a"),
		".claude/skills/scratch/tmp.md":  []byte("b"),
	}, nil)
	snap.IgnorePaths = []string{".claude/skills/scratch/**"}

	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Len(t, p.Steps, 1)
	require.Equal(t, ".claude/CLAUDE.md", p.Steps[0].Rel)
}

// 未定义引用：拒绝该文件并记进 Skip，其余文件照常（spec §6.1）。
func TestPlanRefusesFileWithUndefinedRef(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":     []byte("ok"),
		".claude/settings.json": []byte(`{"K":"{{cred.deleted}}"}`),
	}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Len(t, p.Steps, 1)
	require.Equal(t, ".claude/CLAUDE.md", p.Steps[0].Rel)
	require.Len(t, p.Skip, 1)
	require.Equal(t, ".claude/settings.json", p.Skip[0].Rel)
	require.Contains(t, p.Skip[0].Reason, "cred.deleted")
}

// 缺内容就整体不动：宁可不 apply，也不能只写一半（spec §7.4）。
func TestPlanFailsWhenContentMissing(t *testing.T) {
	snap, _ := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("x")}, nil)
	_, err := applier.BuildPlan(snap, nil, map[string][]byte{}, sec().Lookup)
	require.Error(t, err)
}

// 路径安全在这里也要判一次（spec §3.3：展开与落盘两处都判）。
func TestPlanRejectsUnsafePath(t *testing.T) {
	content := []byte("x")
	h := blobcache.Hash(content)
	snap := protocol.ConfigSnapshot{Files: []protocol.FileEntry{
		{Path: "../escape", Hash: h, Size: 1, Mode: 0o644},
	}}
	_, err := applier.BuildPlan(snap, nil, map[string][]byte{h: content}, sec().Lookup)
	require.Error(t, err)
}

func TestPlanIsSortedByPath(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{
		".claude/z.md": []byte("z"),
		".claude/a.md": []byte("a"),
		".claude.json": []byte("{}"),
	}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, []string{".claude.json", ".claude/a.md", ".claude/z.md"},
		[]string{p.Steps[0].Rel, p.Steps[1].Rel, p.Steps[2].Rel})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/applier/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `agent/internal/applier/plan.go`：

```go
// Package applier 把一份 ConfigSnapshot 落到磁盘上（spec §7.4）。
//
// 这是 Orciny **唯一会写用户文件**的地方，因此整个包的组织都围绕一条规矩：
// 宁可不动，不可写坏。具体化为三级处置——
//  1. 任一路径失败 → 逆序还原本次已改动的全部路径 → RolledBack
//  2. 还原本身也失败 → 进 degraded，此后不再自动 apply 任何版本
//  3. 两种情形都上报，由 hub 记事件、面板告警
package applier

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type Step struct {
	Rel      string
	Action   uint8
	Blob     string      // 渲染前内容的 hash
	Rendered string      // 渲染后内容的 hash
	Mode     os.FileMode //
	Keys     []string    // 仅 merge
	Content  []byte      // 已渲染；skip / delete 时为 nil
}

type Plan struct {
	Steps []Step
	// Skip 是「本该管但这次不动」的路径与原因，会随 ApplyAck 上报。
	Skip []manifest.Skip
}

// Writes 返回真的会碰磁盘的步数。幂等重放时它应当是 0。
func (p Plan) Writes() int {
	n := 0
	for _, s := range p.Steps {
		if s.Action != protocol.ActionSkip {
			n++
		}
	}
	return n
}

// BuildPlan 比对快照与本机状态，给出要做的事。
//
// content 是 hash → 渲染前内容（由 blobcache 与刚收到的 BlobData 拼出来）。
// 缺任何一份就整体失败：宁可不 apply，也不能只写一半。
func BuildPlan(
	snap protocol.ConfigSnapshot,
	st *state.State,
	content map[string][]byte,
	look func(protocol.Ref) (string, bool),
) (Plan, error) {
	var p Plan
	wanted := map[string]bool{}

	for _, f := range snap.Files {
		if err := manifest.SafeRelPath(f.Path); err != nil {
			return Plan{}, fmt.Errorf("applier: 快照里的路径 %q 非法: %w", f.Path, err)
		}
		// 恒排除双侧各判一次：这一侧防的是「一个被篡改的 hub 骗 agent
		// 覆盖 .credentials.json」。这是 agent 唯一能自我保护的地方（spec §3.2）。
		if manifest.IsAlwaysExcluded(f.Path) {
			p.Skip = append(p.Skip, manifest.Skip{Rel: f.Path, Reason: protocol.SkipAlwaysExcluded})
			continue
		}
		if matchAny(snap.IgnorePaths, f.Path) {
			continue // 用户显式忽略的，不进 plan 也不上报
		}
		wanted[f.Path] = true

		raw, ok := content[f.Hash]
		if !ok {
			return Plan{}, fmt.Errorf("applier: 缺少 %s 的内容（hash %s）", f.Path, f.Hash)
		}
		out, err := render.Render(raw, look)
		if err != nil {
			// 未定义引用只废掉这一个文件，其余照常（spec §6.1）。
			p.Skip = append(p.Skip, manifest.Skip{Rel: f.Path, Reason: err.Error()})
			continue
		}

		step := Step{
			Rel:      f.Path,
			Blob:     f.Hash,
			Rendered: blobcache.Hash(out),
			Mode:     modeOf(f, raw),
			Keys:     f.Keys,
			Content:  out,
		}
		switch {
		case len(f.Keys) > 0:
			// keys 模式永远走 merge：是否需要改由合并结果决定，
			// 这里判不了——磁盘上还有 Claude Code 自己写的键。
			step.Action = protocol.ActionMerge
		case st != nil && st.Files[f.Path].Rendered == step.Rendered &&
			st.Files[f.Path].Mode == uint32(step.Mode):
			step.Action = protocol.ActionSkip
			step.Content = nil
		case st != nil && st.Files[f.Path].Rendered != "":
			step.Action = protocol.ActionOverwrite
		default:
			step.Action = protocol.ActionCreate
		}
		p.Steps = append(p.Steps, step)
	}

	// 上一版有、本版无 → delete。
	if st != nil {
		for rel, fs := range st.Files {
			if wanted[rel] || matchAny(snap.IgnorePaths, rel) {
				continue
			}
			p.Steps = append(p.Steps, Step{
				Rel: rel, Action: protocol.ActionDelete,
				Blob: fs.Blob, Rendered: fs.Rendered, Mode: os.FileMode(fs.Mode),
			})
		}
	}

	sort.Slice(p.Steps, func(i, j int) bool { return p.Steps[i].Rel < p.Steps[j].Rel })
	sort.Slice(p.Skip, func(i, j int) bool { return p.Skip[i].Rel < p.Skip[j].Rel })
	return p, nil
}

// modeOf 取权限位。快照给了就用快照的；没给（历史数据）就按
// 「含 cred 引用则 0600，其余 0644」推导（spec §7.4）。
func modeOf(f protocol.FileEntry, raw []byte) os.FileMode {
	if f.Mode != 0 {
		return os.FileMode(f.Mode)
	}
	if refs, err := protocol.Refs(raw); err == nil {
		for _, r := range refs {
			if r.Kind == protocol.RefCred {
				return 0o600
			}
		}
	}
	return 0o644
}

func matchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if manifest.MatchGlob(p, rel) || strings.EqualFold(p, rel) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/applier/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/internal/applier/
git commit -m "feat: apply 计划的五种动作"
```

---

### Task 3: 快照、原子落盘与回滚

**Files:**
- Create: `agent/internal/applier/applier.go`
- Create: `agent/internal/applier/snapshot.go`
- Test: `agent/internal/applier/applier_test.go`

**Interfaces:**
- Produces:
  ```go
  type FS interface {
      Write(path string, data []byte, perm os.FileMode) error
      Read(path string) ([]byte, error)
      Remove(path string) error
      Stat(path string) (os.FileInfo, error)
  }
  func OSFS() FS

  type Options struct {
      Dir         string
      ManagedHome string
      Clock       clock.Clock
      Logger      *slog.Logger
      FS          FS
  }
  type Applier struct{ /* ... */ }
  func New(o Options) *Applier
  func (a *Applier) Apply(snap protocol.ConfigSnapshot, p Plan, st *state.State) (protocol.ApplyAck, *state.State)
  const SnapshotKeep = 5
  ```

**规则（spec §7.4）**

- apply 前把所有将被写 / 删的路径的当前内容**原样**（含真实凭据值）存进 `~/.orciny/snapshots/<unix>-<seq>/`，目录 0700 / 文件 0600，附 `manifest.json` 记录路径与原权限。保留最近 5 次。
- 落盘走 `internal/atomicfile`（写临时文件 + rename）。
- 任一路径失败 → **逆序**还原本次已改动的全部路径 → `ApplyAck{OK:false, RolledBack:true}`。
- 还原本身也失败 → `ApplyAck{OK:false, RolledBack:false}` + `state.Health = degraded`，此后拒绝一切自动 apply。

`FS` 是注入点，测试用它制造写失败——这是唯一能可靠触发回滚路径的办法。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/applier/applier_test.go`：

```go
package applier_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

type fixture struct {
	dir     string
	home    string
	clk     *clock.Fake
	failOn  map[string]bool // 相对路径 → 写它时报错
	applier *applier.Applier
}

// failFS 包一层 OSFS，指定路径的写会失败。这是唯一能可靠触发回滚的办法。
type failFS struct {
	inner applier.FS
	home  string
	fail  map[string]bool
}

func (f failFS) Write(path string, data []byte, perm os.FileMode) error {
	rel, _ := filepath.Rel(f.home, path)
	if f.fail[filepath.ToSlash(rel)] {
		return os.ErrPermission
	}
	return f.inner.Write(path, data, perm)
}
func (f failFS) Read(path string) ([]byte, error)      { return f.inner.Read(path) }
func (f failFS) Remove(path string) error              { return f.inner.Remove(path) }
func (f failFS) Stat(path string) (os.FileInfo, error) { return f.inner.Stat(path) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fx := &fixture{
		dir:    t.TempDir(),
		home:   t.TempDir(),
		clk:    clock.NewFake(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)),
		failOn: map[string]bool{},
	}
	fx.applier = applier.New(applier.Options{
		Dir:         fx.dir,
		ManagedHome: fx.home,
		Clock:       fx.clk,
		FS:          failFS{inner: applier.OSFS(), home: fx.home, fail: fx.failOn},
	})
	return fx
}

func (fx *fixture) write(t *testing.T, rel, content string, perm os.FileMode) {
	t.Helper()
	p := filepath.Join(fx.home, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), perm))
}

func (fx *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fx.home, rel))
	require.NoError(t, err)
	return string(b)
}

func TestApplyCreatesAndOverwrites(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/CLAUDE.md", "旧内容", 0o644)

	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":     []byte("新内容"),
		".claude/settings.json": []byte(`{"K":"{{cred.k}}"}`),
	}, map[string]uint32{".claude/settings.json": 0o600})

	st := &state.State{Files: map[string]state.FileState{
		".claude/CLAUDE.md": {Blob: "x", Rendered: blobcache.Hash([]byte("旧内容")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, next := fx.applier.Apply(snap, p, st)
	require.True(t, ack.OK, "错误：%s", ack.Error)
	require.Equal(t, "新内容", fx.read(t, ".claude/CLAUDE.md"))
	require.Equal(t, `{"K":"sk-real-value"}`, fx.read(t, ".claude/settings.json"))

	// 权限位：含凭据 0600，其余 0644（spec §7.4）
	info, err := os.Stat(filepath.Join(fx.home, ".claude/settings.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	info, err = os.Stat(filepath.Join(fx.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	require.Equal(t, snap.RevisionID, next.Revision)
	require.Equal(t, snap.Checksum, next.Checksum)
	require.Equal(t, state.HealthOK, next.Health)
	require.Equal(t, blobcache.Hash([]byte("新内容")), next.Files[".claude/CLAUDE.md"].Rendered)
}

func TestApplyDeletes(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/gone.md", "待删", 0o644)

	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("keep")}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/gone.md": {Blob: "a", Rendered: blobcache.Hash([]byte("待删")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, next := fx.applier.Apply(snap, p, st)
	require.True(t, ack.OK)
	require.NoFileExists(t, filepath.Join(fx.home, ".claude/gone.md"))
	require.NotContains(t, next.Files, ".claude/gone.md")
}

// 幂等重放：同一 revision apply 两次，第二次全 skip、零写入（spec §7.3）。
func TestApplyIsIdempotent(t *testing.T) {
	fx := newFixture(t)
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("内容")}, nil)

	p1, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	_, st1 := fx.applier.Apply(snap, p1, nil)

	p2, err := applier.BuildPlan(snap, st1, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, 0, p2.Writes(), "第二次必须零写入")

	before, err := os.Stat(filepath.Join(fx.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p2, st1)
	require.True(t, ack.OK)
	after, err := os.Stat(filepath.Join(fx.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime(), "skip 不该碰文件")
}

// 失败回滚：注入一个写失败，断言全部路径回到 apply 前状态（spec §7.4）。
func TestApplyRollsBackOnFailure(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/CLAUDE.md", "原始 A", 0o644)
	fx.write(t, ".claude/b.md", "原始 B", 0o644)
	fx.failOn[".claude/z-fails.md"] = true

	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":  []byte("新 A"),
		".claude/b.md":       []byte("新 B"),
		".claude/z-fails.md": []byte("写不进去"),
	}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/CLAUDE.md": {Blob: "x", Rendered: blobcache.Hash([]byte("原始 A")), Mode: 0o644},
		".claude/b.md":      {Blob: "y", Rendered: blobcache.Hash([]byte("原始 B")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, next := fx.applier.Apply(snap, p, st)
	require.False(t, ack.OK)
	require.True(t, ack.RolledBack)
	require.Equal(t, "原始 A", fx.read(t, ".claude/CLAUDE.md"), "必须回到 apply 前")
	require.Equal(t, "原始 B", fx.read(t, ".claude/b.md"))
	require.NoFileExists(t, filepath.Join(fx.home, ".claude/z-fails.md"))
	require.Equal(t, st.Revision, next.Revision, "回滚成功时 state 不前进")
	require.Equal(t, state.HealthOK, next.Health, "回滚成功不进 degraded")
}

// 回滚本身失败 → degraded，此后拒绝一切自动 apply（spec §7.4 第 2 条）。
func TestRollbackFailureEntersDegraded(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/a.md", "原始 A", 0o644)
	// a.md 先被成功改写，随后 z 失败触发回滚——但回滚要重写 a.md，
	// 而此刻 a.md 也写不动了。
	fx.failOn[".claude/z-fails.md"] = true

	snap, blobs := snapFor(map[string][]byte{
		".claude/a.md":       []byte("新 A"),
		".claude/z-fails.md": []byte("写不进去"),
	}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/a.md": {Blob: "x", Rendered: blobcache.Hash([]byte("原始 A")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	// 让 a.md 的**回滚写**也失败：在 z 失败之后才把 a.md 加进 failOn。
	// failFS 读的是同一张 map，applier 逐步执行，因此这里直接把两个都设上，
	// 并让 a.md 的第一次写走「先成功再失败」——用计数器实现。
	fx.failOn[".claude/a.md"] = false

	ack, next := fx.applier.ApplyWithHook(snap, p, st, func(rel string, done int) {
		if rel == ".claude/a.md" {
			fx.failOn[".claude/a.md"] = true // 之后的回滚写会失败
		}
	})
	require.False(t, ack.OK)
	require.False(t, ack.RolledBack, "回滚没成功")
	require.Equal(t, state.HealthDegraded, next.Health)

	// degraded 之后拒绝一切自动 apply
	ack2, _ := fx.applier.Apply(snap, p, next)
	require.False(t, ack2.OK)
	require.Contains(t, ack2.Error, "degraded")
}

// apply 前快照：所有将被写/删的路径的当前内容原样留一份（spec §7.4）。
func TestSnapshotIsTakenBeforeWriting(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/settings.json", `{"K":"sk-真实密钥"}`, 0o600)

	snap, blobs := snapFor(map[string][]byte{
		".claude/settings.json": []byte(`{"K":"{{cred.k}}"}`),
	}, map[string]uint32{".claude/settings.json": 0o600})
	st := &state.State{Files: map[string]state.FileState{
		".claude/settings.json": {Blob: "x", Rendered: "old", Mode: 0o600},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, st)
	require.True(t, ack.OK)

	entries, err := os.ReadDir(filepath.Join(fx.dir, "snapshots"))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	snapDir := filepath.Join(fx.dir, "snapshots", entries[0].Name())
	info, err := os.Stat(snapDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	saved, err := os.ReadFile(filepath.Join(snapDir, ".claude/settings.json"))
	require.NoError(t, err)
	require.Equal(t, `{"K":"sk-真实密钥"}`, string(saved), "快照存的是原样内容，含真实值")
	require.FileExists(t, filepath.Join(snapDir, "manifest.json"))
}

func TestSnapshotsKeepOnly5(t *testing.T) {
	fx := newFixture(t)
	for i := range 8 {
		fx.clk.Advance(time.Second)
		snap, blobs := snapFor(map[string][]byte{
			".claude/CLAUDE.md": []byte(string(rune('a' + i))),
		}, nil)
		st, _ := state.Load(fx.dir)
		p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
		require.NoError(t, err)
		ack, next := fx.applier.Apply(snap, p, st)
		require.True(t, ack.OK)
		require.NoError(t, state.Save(fx.dir, next))
	}
	entries, err := os.ReadDir(filepath.Join(fx.dir, "snapshots"))
	require.NoError(t, err)
	require.Len(t, entries, applier.SnapshotKeep)
}

func TestAckCarriesResultsAndDuration(t *testing.T) {
	fx := newFixture(t)
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("x")}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK)
	require.Equal(t, snap.RevisionID, ack.RevisionID)
	require.Len(t, ack.Results, 1)
	require.Equal(t, protocol.ActionCreate, ack.Results[0].Action)
	require.Equal(t, ".claude/CLAUDE.md", ack.Results[0].Path)
}
```

> `ApplyWithHook` 只为 `TestRollbackFailureEntersDegraded` 存在：回滚失败必须能被测到，而它要求「同一路径先能写、后不能写」。把 hook 做成导出方法而不是测试专用文件，是因为 `Apply` 本身就是它的零 hook 版本（`Apply` = `ApplyWithHook(..., nil)`），没有多出一条只在测试里跑的分支。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/applier/ -run Apply -v`
Expected: FAIL，`undefined: applier.New`

- [ ] **Step 3: 实现快照**

Create `agent/internal/applier/snapshot.go`：

```go
package applier

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// SnapshotDirName 是快照根目录名，SnapshotKeep 是保留份数（spec §7.4）。
const (
	SnapshotDirName = "snapshots"
	SnapshotKeep    = 5
)

// snapshotManifest 记录每条路径的原始权限，回滚时按它还原。
type snapshotManifest struct {
	Revision string            `json:"revision"`
	Paths    map[string]uint32 `json:"paths"`   // rel → 原权限；不存在的路径值为 0
	Absent   []string          `json:"absent"`  // apply 前本就不存在，回滚时要删掉
}

// takeSnapshot 把所有将被写/删的路径的当前内容原样存一份。
//
// 「原样」包含真实凭据值——快照是给回滚用的，必须能一字不差地还原现场。
// 因此目录 0700、文件 0600。
func (a *Applier) takeSnapshot(revision string, p Plan) (string, *snapshotManifest, error) {
	dir := filepath.Join(a.o.Dir, SnapshotDirName,
		fmt.Sprintf("%d-%s", a.o.Clock.Now().Unix(), shortID(revision)))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("applier: 建快照目录: %w", err)
	}

	man := &snapshotManifest{Revision: revision, Paths: map[string]uint32{}}
	for _, s := range p.Steps {
		if s.Action == protocol.ActionSkip {
			continue
		}
		abs := filepath.Join(a.o.ManagedHome, filepath.FromSlash(s.Rel))
		info, err := a.fs.Stat(abs)
		if err != nil {
			man.Absent = append(man.Absent, s.Rel)
			continue
		}
		content, err := a.fs.Read(abs)
		if err != nil {
			return "", nil, fmt.Errorf("applier: 快照 %s: %w", s.Rel, err)
		}
		if err := atomicfile.Write(filepath.Join(dir, filepath.FromSlash(s.Rel)), content, 0o600); err != nil {
			return "", nil, fmt.Errorf("applier: 写快照 %s: %w", s.Rel, err)
		}
		man.Paths[s.Rel] = uint32(info.Mode().Perm())
	}

	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("applier: 序列化快照清单: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(dir, "manifest.json"), b, 0o600); err != nil {
		return "", nil, fmt.Errorf("applier: 写快照清单: %w", err)
	}
	a.pruneSnapshots()
	return dir, man, nil
}

// pruneSnapshots 只保留最近 SnapshotKeep 份。目录名以 unix 时间戳打头，
// 字典序即时间序。
func (a *Applier) pruneSnapshots() {
	root := filepath.Join(a.o.Dir, SnapshotDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) <= SnapshotKeep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-SnapshotKeep] {
		if err := os.RemoveAll(filepath.Join(root, n)); err != nil {
			a.log.Warn("清理旧快照失败", "dir", n, "error", err)
		}
	}
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	if s == "" {
		return "none"
	}
	return s
}
```

补 import `"github.com/FlintyLemming/orciny/protocol"`。

- [ ] **Step 4: 实现 applier 主体**

Create `agent/internal/applier/applier.go`：

```go
package applier

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// FS 是 applier 对文件系统的全部需求。做成接口只有一个理由：
// 「注入一个写失败」是唯一能可靠测到回滚路径的办法，而回滚正是
// 这个包存在的意义。
type FS interface {
	Write(path string, data []byte, perm os.FileMode) error
	Read(path string) ([]byte, error)
	Remove(path string) error
	Stat(path string) (os.FileInfo, error)
}

type osFS struct{}

func OSFS() FS { return osFS{} }

func (osFS) Write(path string, data []byte, perm os.FileMode) error {
	return atomicfile.Write(path, data, perm)
}
func (osFS) Read(path string) ([]byte, error)      { return os.ReadFile(path) }
func (osFS) Remove(path string) error              { return os.Remove(path) }
func (osFS) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

type Options struct {
	Dir         string // agent 目录（~/.orciny）
	ManagedHome string
	Clock       clock.Clock
	Logger      *slog.Logger
	FS          FS
}

type Applier struct {
	o   Options
	fs  FS
	log *slog.Logger
}

func New(o Options) *Applier {
	if o.Clock == nil {
		o.Clock = clock.System()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.FS == nil {
		o.FS = OSFS()
	}
	return &Applier{o: o, fs: o.FS, log: o.Logger}
}

// Apply 执行计划。返回回执与**下一份** state（调用方负责落盘）。
func (a *Applier) Apply(snap protocol.ConfigSnapshot, p Plan, st *state.State) (protocol.ApplyAck, *state.State) {
	return a.ApplyWithHook(snap, p, st, nil)
}

// ApplyWithHook 与 Apply 相同，额外在每一步写盘成功后调用 hook。
// hook 只有测试会传（用来在中途改变文件系统的行为）。
func (a *Applier) ApplyWithHook(
	snap protocol.ConfigSnapshot, p Plan, st *state.State,
	hook func(rel string, done int),
) (protocol.ApplyAck, *state.State) {
	start := a.o.Clock.Now()
	ack := protocol.ApplyAck{RevisionID: snap.RevisionID}

	next := cloneState(st)
	if next == nil {
		next = &state.State{Files: map[string]state.FileState{}, Health: state.HealthOK}
	}

	// degraded 是终态，只有人工解除才出来（spec §7.4 第 2 条）：
	// 一次写坏之后继续按新版本去写，只会把现场破坏得更彻底。
	if next.Health == state.HealthDegraded {
		ack.Error = "本机处于 degraded 状态，已停止自动 apply，请在 Web 上确认解除"
		return ack, next
	}
	if next.Paused {
		ack.Error = "本机已暂停配置管理（orciny-agent pause）"
		return ack, next
	}

	snapDir, man, err := a.takeSnapshot(snap.RevisionID, p)
	if err != nil {
		ack.Error = err.Error()
		return ack, next
	}

	// applied 记录本次真的改动过的路径，失败时按逆序还原。
	var applied []string
	for _, s := range p.Steps {
		res := protocol.ApplyResult{Path: s.Rel, Action: s.Action}
		if s.Action == protocol.ActionSkip {
			ack.Results = append(ack.Results, res)
			continue
		}

		abs, perr := manifest.ResolveUnder(a.o.ManagedHome, s.Rel)
		if perr != nil {
			res.Error = perr.Error()
			ack.Results = append(ack.Results, res)
			return a.fail(ack, next, snapDir, man, applied, start, perr)
		}

		var werr error
		switch s.Action {
		case protocol.ActionCreate, protocol.ActionOverwrite:
			werr = a.fs.Write(abs, s.Content, s.Mode)
		case protocol.ActionDelete:
			werr = a.fs.Remove(abs)
			if os.IsNotExist(werr) {
				werr = nil // 已经不在了，正是想要的结果
			}
		case protocol.ActionMerge:
			werr = a.merge(abs, s)
		}
		if werr != nil {
			res.Error = werr.Error()
			ack.Results = append(ack.Results, res)
			return a.fail(ack, next, snapDir, man, applied, start, werr)
		}

		applied = append(applied, s.Rel)
		ack.Results = append(ack.Results, res)
		if hook != nil {
			hook(s.Rel, len(applied))
		}

		if s.Action == protocol.ActionDelete {
			delete(next.Files, s.Rel)
			continue
		}
		next.Files[s.Rel] = state.FileState{
			Blob:     s.Blob,
			Rendered: a.renderedOnDisk(abs, s),
			Mode:     uint32(s.Mode),
			Size:     uint32(len(s.Content)),
			Keys:     s.Keys,
		}
	}

	ack.OK = true
	ack.DurationMs = uint32(a.o.Clock.Now().Sub(start) / time.Millisecond)
	next.ConfigSet = snap.ConfigSetID
	next.Revision = snap.RevisionID
	next.Seq = snap.Seq
	next.Checksum = snap.Checksum
	next.AppliedAt = a.o.Clock.Now().UTC()
	next.Mode = modeName(snap.Mode)
	next.Health = state.HealthOK
	next.Ignored = snap.IgnorePaths
	return ack, next
}

// fail 走回滚路径。逆序还原是必须的：后写的可能依赖先写的（同一目录），
// 逆序还原让中间态存在的时间最短。
func (a *Applier) fail(
	ack protocol.ApplyAck, next *state.State,
	snapDir string, man *snapshotManifest, applied []string,
	start time.Time, cause error,
) (protocol.ApplyAck, *state.State) {
	ack.OK = false
	ack.Error = cause.Error()
	ack.DurationMs = uint32(a.o.Clock.Now().Sub(start) / time.Millisecond)

	if err := a.rollback(snapDir, man, applied); err != nil {
		// 第二级处置：还原都失败了，进 degraded 并停止自动 apply。
		a.log.Error("回滚失败，进入 degraded", "error", err, "cause", cause)
		ack.RolledBack = false
		next.Health = state.HealthDegraded
		return ack, next
	}
	a.log.Warn("apply 失败，已回滚到 apply 前状态", "error", cause)
	ack.RolledBack = true
	return ack, next
}

// rollback 逆序还原 applied 里的每一条。
func (a *Applier) rollback(snapDir string, man *snapshotManifest, applied []string) error {
	absent := map[string]bool{}
	for _, rel := range man.Absent {
		absent[rel] = true
	}
	for i := len(applied) - 1; i >= 0; i-- {
		rel := applied[i]
		abs := filepath.Join(a.o.ManagedHome, filepath.FromSlash(rel))
		if absent[rel] {
			if err := a.fs.Remove(abs); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("删除 %s: %w", rel, err)
			}
			continue
		}
		content, err := a.fs.Read(filepath.Join(snapDir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("读快照 %s: %w", rel, err)
		}
		perm := os.FileMode(man.Paths[rel])
		if perm == 0 {
			perm = 0o644
		}
		if err := a.fs.Write(abs, content, perm); err != nil {
			return fmt.Errorf("还原 %s: %w", rel, err)
		}
	}
	return nil
}

// renderedOnDisk 取落盘后的实际内容 hash。
// merge 之后磁盘内容不等于 s.Content（还有未受管的键），必须重读。
func (a *Applier) renderedOnDisk(abs string, s Step) string {
	if s.Action != protocol.ActionMerge {
		return s.Rendered
	}
	b, err := a.fs.Read(abs)
	if err != nil {
		return ""
	}
	return blobcache.Hash(b)
}

func cloneState(st *state.State) *state.State {
	if st == nil {
		return nil
	}
	c := *st
	c.Files = make(map[string]state.FileState, len(st.Files))
	for k, v := range st.Files {
		c.Files[k] = v
	}
	return &c
}

func modeName(m uint8) string {
	if m == protocol.ModeSurvey {
		return "survey"
	}
	return "apply"
}
```

补 import `"github.com/FlintyLemming/orciny/agent/internal/blobcache"`。

`merge` 的实现在下一个 Task；先加一个占位让本 Task 的测试跑起来：

```go
// merge 在 Task 4 实现。
func (a *Applier) merge(abs string, s Step) error {
	return fmt.Errorf("applier: keys 模式尚未实现")
}
```

- [ ] **Step 5: 跑测试确认通过（merge 相关用例暂时不跑）**

Run: `go test -tags=testing ./agent/internal/applier/ -run 'TestApply|TestSnapshot|TestRollback|TestAck|TestPlan' -v`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add agent/internal/applier/
git commit -m "feat: apply 的快照、原子落盘与失败回滚"
```

---

### Task 4: `keys` 模式的合并

**Files:**
- Create: `agent/internal/applier/merge.go`
- Test: `agent/internal/applier/merge_test.go`

**Interfaces:**
- Consumes: `github.com/tidwall/sjson` / `gjson`
- Produces: `func (a *Applier) merge(abs string, s Step) error`（替换 Task 3 的占位）

**规则（spec §7.5）**

- **只改受管键、其余字节原样保留**。用 `sjson.SetRaw` 做原地编辑，而不是 `json.Unmarshal` 到 map 再 Marshal 回去——后者会丢掉键顺序与格式，让用户每次打开文件都发现被整个重排了。
- 竞写缓解：读之前记 mtime + size，写之前再查一次；变了就重读重试，**最多 3 次**。
- 目标文件不存在时，从 `{}` 起手。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/applier/merge_test.go`：

```go
package applier_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/protocol"
)

func keysSnap(t *testing.T, content string) (protocol.ConfigSnapshot, map[string][]byte) {
	t.Helper()
	h := blobcache.Hash([]byte(content))
	return protocol.ConfigSnapshot{
		ConfigSetID: "set", RevisionID: "rev", Seq: 1,
		Files: []protocol.FileEntry{{
			Path: ".claude.json", Hash: h, Size: uint32(len(content)),
			Mode: 0o644, Keys: []string{"mcpServers"},
		}},
	}, map[string][]byte{h: []byte(content)}
}

// DoD 第 7 条：非受管键仍在，且键顺序未被重排。
func TestMergeKeepsUnmanagedKeysAndOrder(t *testing.T) {
	fx := newFixture(t)
	original := `{
  "numStartups": 42,
  "mcpServers": {
    "old": {"command": "old"}
  },
  "projects": {
    "/Users/x": {"lastCost": 1.5}
  }
}`
	fx.write(t, ".claude.json", original, 0o644)

	snap, blobs := keysSnap(t, `{"mcpServers":{"new":{"command":"new"}}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK, "错误：%s", ack.Error)

	got := fx.read(t, ".claude.json")
	require.Contains(t, got, `"numStartups": 42`, "非受管键必须原样保留")
	require.Contains(t, got, `"lastCost": 1.5`)
	require.Contains(t, got, `"new"`)
	require.NotContains(t, got, `"old"`, "受管键整体被替换")

	// 键顺序：numStartups 仍在 mcpServers 之前，projects 仍在最后
	require.Less(t, strings.Index(got, "numStartups"), strings.Index(got, "mcpServers"))
	require.Less(t, strings.Index(got, "mcpServers"), strings.Index(got, "projects"))
}

func TestMergeCreatesFileWhenAbsent(t *testing.T) {
	fx := newFixture(t)
	snap, blobs := keysSnap(t, `{"mcpServers":{"a":{"command":"a"}}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK, "错误：%s", ack.Error)
	require.Contains(t, fx.read(t, ".claude.json"), `"a"`)
}

// 受管键在期望内容里缺席 = 该键应当被清空为空对象，而不是保持原状。
func TestMergeClearsManagedKeyWhenAbsentInRevision(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude.json", `{"mcpServers":{"stale":{}},"other":1}`, 0o644)

	snap, blobs := keysSnap(t, `{}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK, "错误：%s", ack.Error)

	got := fx.read(t, ".claude.json")
	require.NotContains(t, got, "stale")
	require.Contains(t, got, `"other"`)
}

func TestMergeIsIdempotent(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude.json", `{"other":1,"mcpServers":{"a":{}}}`, 0o644)

	snap, blobs := keysSnap(t, `{"mcpServers":{"a":{}}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, st := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK)
	first := fx.read(t, ".claude.json")

	p2, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	ack2, _ := fx.applier.Apply(snap, p2, st)
	require.True(t, ack2.OK)
	require.Equal(t, first, fx.read(t, ".claude.json"), "内容一致时不该改动文件")
}

func TestMergeRejectsInvalidJSONOnDisk(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude.json", `{这不是 json`, 0o644)

	snap, blobs := keysSnap(t, `{"mcpServers":{}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.False(t, ack.OK, "磁盘上的 JSON 坏了就不能动它——改写会毁掉用户的数据")
	require.True(t, ack.RolledBack)
	require.Equal(t, `{这不是 json`, fx.read(t, ".claude.json"))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/applier/ -run Merge -v`
Expected: FAIL，`keys 模式尚未实现`

- [ ] **Step 3: 实现**

Create `agent/internal/applier/merge.go`，并删掉 `applier.go` 里的 `merge` 占位：

```go
package applier

import (
	"fmt"
	"os"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// mergeRetries 是竞写重试次数（spec §7.5）。
//
// Claude Code 自己也在写 ~/.claude.json（记录 project 状态等运行时数据），
// 而且它不用原子写。这里无法根治，只能缓解：读之前记 mtime + size，
// 写之前再查一次；变了就重读重试。
const mergeRetries = 3

// merge 只改受管键，其余字节原样保留。
//
// 用 sjson.SetRaw 做原地编辑而不是 Unmarshal 到 map 再 Marshal 回去：
// 后者会丢掉键顺序与格式，让用户每次打开文件都发现被整个重排了（spec §7.5）。
func (a *Applier) merge(abs string, s Step) error {
	for attempt := 0; attempt < mergeRetries; attempt++ {
		before, existed, err := a.statFingerprint(abs)
		if err != nil {
			return err
		}

		current := []byte("{}")
		if existed {
			current, err = a.fs.Read(abs)
			if err != nil {
				return fmt.Errorf("applier: 读取 %s: %w", s.Rel, err)
			}
		}
		if !gjson.ValidBytes(current) {
			// 磁盘上的 JSON 已经坏了就不能动它：sjson 会在一份坏结构上
			// 产出另一份坏结构，用户的 projects 记录就没了。
			return fmt.Errorf("applier: %s 不是合法 JSON，拒绝改写", s.Rel)
		}
		if !gjson.ValidBytes(s.Content) {
			return fmt.Errorf("applier: %s 的目标内容不是合法 JSON", s.Rel)
		}

		out := current
		for _, key := range s.Keys {
			want := gjson.GetBytes(s.Content, key)
			raw := "{}"
			if want.Exists() {
				raw = want.Raw
			}
			out, err = sjson.SetRawBytes(out, key, []byte(raw))
			if err != nil {
				return fmt.Errorf("applier: 合并 %s 的 %s: %w", s.Rel, key, err)
			}
		}

		if existed && string(out) == string(current) {
			return nil // 内容一致，不碰文件（幂等）
		}

		after, _, err := a.statFingerprint(abs)
		if err != nil {
			return err
		}
		if after != before {
			// 读到写之间文件被改过，重来。
			a.log.Debug("合并期间文件被改动，重试", "path", s.Rel, "attempt", attempt+1)
			continue
		}
		// rename 是原子的，写这一半没有中间态。
		return a.fs.Write(abs, out, s.Mode)
	}
	return fmt.Errorf("applier: %s 连续 %d 次在合并期间被改动，本次放弃"+
		"（下一次对账会重试）", s.Rel, mergeRetries)
}

// statFingerprint 返回 mtime+size 的指纹，用于检测竞写。
func (a *Applier) statFingerprint(abs string) (string, bool, error) {
	info, err := a.fs.Stat(abs)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("applier: stat %s: %w", abs, err)
	}
	return fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size()), true, nil
}
```

> 校验一律走 `gjson.ValidBytes`，**不要** `json.Unmarshal` 到 map 再 Marshal 回去——那正是会重排键顺序的写法。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/applier/...`
Expected: PASS，含 `TestRollbackFailureEntersDegraded`

- [ ] **Step 5: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add agent/internal/applier/
git commit -m "feat: keys 模式的原地 JSON 合并与竞写缓解"
```

---

### Task 5: 运维文档记录已知限制

**Files:**
- Modify: `docs/operations.md`（若不存在则按 M0 计划 9 建立的实际文件名调整）

- [ ] **Step 1: 追加一节**

```markdown
## 已知限制：`~/.claude.json` 的竞写

Claude Code 自己也在写 `~/.claude.json`（记录 project 状态等运行时数据），
并且它不使用原子写。orciny-agent 在合并受管键（默认只有 `mcpServers`）时
会做三件事缓解：读之前记 mtime + size、写之前再查一次、变了就重读重试
（最多 3 次），最终写入走「临时文件 + rename」。

**仍然存在的窗口**：Claude Code 在 agent 完成检查到 rename 之间的那一瞬间
重写该文件时，agent 的写入可能被覆盖。下一次对账（默认 ≤5 分钟）会发现
并重新应用。

**inotify watch 数**：`skills/**` 递归监视通常占用几十个 watch，
远低于 Linux 默认的 8192 上限（`/proc/sys/fs/inotify/max_user_watches`）。
若同一台机器上跑了多个大量占用 watch 的工具而报 `no space left on device`，
调高该内核参数即可。
```

- [ ] **Step 2: 提交**

```bash
git add docs/
git commit -m "docs: 记录 .claude.json 竞写与 inotify 上限"
```
