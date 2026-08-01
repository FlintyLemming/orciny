# 子计划 13 · 漂移上报链路与 hub 侧 diff

**前置**：08、12
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：agent 侧把 watcher 接上连接、hub 侧 `drift.Service.HandleReport`、unified diff 计算、`drift_events` 落库与部分唯一索引的 upsert 语义。

**为什么 diff 由 hub 算（spec §8.2）**：agent 更瘦（M1 之后它还要背 M2 的采集器，每一克都要省）；diff 算法只有一份实现，前后端口径天然一致；**收编退化成纯 hub 侧操作**——用已有的 blob 组装新 Revision 就完事，不需要「通知机器上传内容」的第二次往返，三方对比也立即可用。代价是每次漂移上报多传几 KB，配置文件都很小，划算。

**新增依赖**：`github.com/pmezard/go-difflib`（已在 `go.sum`，转直接依赖）。

---

### Task 1: agent 侧接线

**Files:**
- Modify: `agent/internal/syncer/syncer.go`（加 `Report`、启动 watcher、处理 `DriftCommand`）
- Modify: `agent/run.go`
- Test: `agent/internal/syncer/drift_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s *Syncer) Report(items []protocol.DriftItem, full bool) error
  func (s *Syncer) StartWatcher(ctx context.Context) error
  ```

**分批（spec §5.4）**：`DriftReport` 按累计 256 KiB 分批，最后一批置 `Final`。

**`DriftCommand`（spec §8.4）**

- `restore`：按 `state.json` 基线重写这些路径，**走完整的快照 + 原子写 + 回滚流程**——恢复和 apply 一样会写用户文件，不该有第二条更宽松的路径。
- `ignore`：本地跳过，等下次快照带下来的 `IgnorePaths` 固化。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/syncer/drift_test.go`：

```go
package syncer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func driftReports(t *testing.T, r *rig) []protocol.DriftReport {
	t.Helper()
	var out []protocol.DriftReport
	for _, p := range r.out.of(protocol.KindDriftReport) {
		out = append(out, p.(protocol.DriftReport))
	}
	return out
}

func TestReportSendsSingleBatch(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.s.Report([]protocol.DriftItem{
		{Path: "a", Kind: protocol.DriftModified, Content: []byte("x")},
	}, false))

	res := driftReports(t, r)
	require.Len(t, res, 1)
	require.True(t, res[0].Final)
	require.False(t, res[0].Full)
	require.Len(t, res[0].Items, 1)
}

func TestReportBatchesLargePayload(t *testing.T) {
	r := newRig(t)
	var items []protocol.DriftItem
	for i := range 5 {
		items = append(items, protocol.DriftItem{
			Path:    filepath.Join(".claude/skills", string(rune('a'+i)), "SKILL.md"),
			Kind:    protocol.DriftModified,
			Content: []byte(strings.Repeat("x", 100*1024)),
		})
	}
	require.NoError(t, r.s.Report(items, true))

	res := driftReports(t, r)
	require.Greater(t, len(res), 1, "500 KiB 必须分批")
	for i, batch := range res {
		total := 0
		for _, it := range batch.Items {
			total += len(it.Content)
		}
		require.LessOrEqual(t, total, protocol.MaxBatchSize)
		require.True(t, batch.Full, "Full 标记要贯穿每一批")
		require.Equal(t, i == len(res)-1, batch.Final)
	}
}

// 恢复走完整的 apply 流程：快照 + 原子写 + 失败回滚。
func TestDriftCommandRestoreRewritesFromBaseline(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "基线内容\n"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	require.Equal(t, "基线内容\n", readManaged(t, r, ".claude/CLAUDE.md"))

	// 用户改了它
	writeManaged(t, r, ".claude/CLAUDE.md", "被改过了\n")

	r.s.Handle(envelope(t, protocol.KindDriftCommand,
		protocol.DriftCommand{Op: protocol.OpRestore, Paths: []string{".claude/CLAUDE.md"}}))

	require.Equal(t, "基线内容\n", readManaged(t, r, ".claude/CLAUDE.md"))

	// 恢复也要回执，hub 靠它把漂移置 restored
	acks := r.out.of(protocol.KindApplyAck)
	require.NotEmpty(t, acks)
	require.True(t, acks[len(acks)-1].(protocol.ApplyAck).OK)

	// 快照目录里应当留着「被改过了」那一版
	entries, err := os.ReadDir(filepath.Join(r.dir, "snapshots"))
	require.NoError(t, err)
	require.NotEmpty(t, entries)
}

// 基线里没有这条路径（added 类漂移）→ 恢复即删除。
func TestDriftCommandRestoreDeletesAddedFile(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "基线\n"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	writeManaged(t, r, ".claude/skills/新的/SKILL.md", "本地新增\n")

	r.s.Handle(envelope(t, protocol.KindDriftCommand,
		protocol.DriftCommand{Op: protocol.OpRestore, Paths: []string{".claude/skills/新的/SKILL.md"}}))

	require.NoFileExists(t, filepath.Join(r.home, ".claude/skills/新的/SKILL.md"))
}

func TestDriftCommandIgnoreSkipsPath(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "基线\n"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	writeManaged(t, r, ".claude/CLAUDE.md", "改过了\n")

	r.s.Handle(envelope(t, protocol.KindDriftCommand,
		protocol.DriftCommand{Op: protocol.OpIgnore, Paths: []string{".claude/CLAUDE.md"}}))

	// 忽略之后再对账，这条不该再上报
	require.NoError(t, r.s.ReconcileNow())
	for _, rep := range driftReports(t, r) {
		for _, it := range rep.Items {
			require.NotEqual(t, ".claude/CLAUDE.md", it.Path)
		}
	}
	require.Equal(t, "改过了\n", readManaged(t, r, ".claude/CLAUDE.md"), "忽略不改文件")
}
```

`readManaged` 是 `syncer_test.go` 里的小辅助（与 `writeManaged` 成对）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/syncer/ -run 'Report|DriftCommand' -v`
Expected: FAIL，`s.Report undefined`

- [ ] **Step 3: 实现**

Create `agent/internal/syncer/drift.go`：

```go
package syncer

import (
	"context"
	"fmt"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Report 上报漂移，按累计 256 KiB 分批，最后一批置 Final（spec §5.4）。
//
// Full 标记贯穿每一批：hub 用它区分「增量上报」与「survey 的全量对账」，
// 后者要把没出现在报告里的旧漂移标记为已解决。
func (s *Syncer) Report(items []protocol.DriftItem, full bool) error {
	if len(items) == 0 {
		return nil
	}
	var (
		batch []protocol.DriftItem
		size  int
	)
	flush := func(final bool) error {
		err := s.d.Send(protocol.KindDriftReport, protocol.DriftReport{
			Items: batch, Final: final, Full: full,
		})
		batch, size = nil, 0
		return err
	}
	for _, it := range items {
		if size+len(it.Content) > protocol.MaxBatchSize && len(batch) > 0 {
			if err := flush(false); err != nil {
				return err
			}
		}
		batch = append(batch, it)
		size += len(it.Content)
	}
	return flush(true)
}

// StartWatcher 起文件监视与定时对账。ctx 取消时干净退出。
func (s *Syncer) StartWatcher(ctx context.Context) error {
	w, err := watcher.New(watcher.Options{
		Dir:         s.d.Dir,
		ManagedHome: s.d.ManagedHome,
		Clock:       s.d.Clock,
		Reconcile:   s.d.ReconcileInterval,
		Logger:      s.log,
		Report:      s.Report,
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.watcher = w
	st, sec := s.st, s.sec
	s.mu.Unlock()

	if st != nil {
		if m, err := s.manifestOf(st); err == nil {
			_ = w.Reload(st, m, sec)
		}
	}
	return w.Run(ctx)
}

// ReconcileNow 立即做一次全量对账并上报（CLI 的 sync / drift 用）。
func (s *Syncer) ReconcileNow() error {
	s.mu.Lock()
	w := s.watcher
	s.mu.Unlock()
	if w == nil {
		return nil
	}
	items, err := w.Scan(true)
	if err != nil {
		return err
	}
	return s.Report(items, true)
}

// onDriftCommand 处理 hub 下达的恢复 / 忽略（spec §8.4）。
func (s *Syncer) onDriftCommand(cmd protocol.DriftCommand) {
	switch cmd.Op {
	case protocol.OpIgnore:
		// 忽略只改本地视图，不碰文件。持久化靠下一份快照带下来的
		// IgnorePaths——真相在 hub 的 ignore_rules 里。
		s.mu.Lock()
		if s.st != nil {
			s.st.Ignored = append(s.st.Ignored, cmd.Paths...)
			_ = state.Save(s.d.Dir, s.st)
		}
		st, sec := s.st, s.sec
		w := s.watcher
		s.mu.Unlock()
		if w != nil && st != nil {
			if m, err := s.manifestOf(st); err == nil {
				_ = w.Reload(st, m, sec)
			}
		}

	case protocol.OpRestore:
		s.restore(cmd.Paths)

	default:
		s.log.Warn("未知的漂移指令", "op", cmd.Op)
	}
}

// restore 按 state.json 基线重写这些路径。
//
// 走的是完整的 apply 流程（快照 + 原子写 + 失败回滚）：恢复和 apply 一样
// 会写用户文件，不该有第二条更宽松的路径（spec §8.4）。
func (s *Syncer) restore(paths []string) {
	s.mu.Lock()
	st := s.st
	s.mu.Unlock()
	if st == nil {
		s.log.Warn("本机没有基线，无法恢复")
		return
	}

	sec, err := secrets.Load(s.d.Dir)
	if err != nil {
		s.log.Error("读取凭据缓存失败", "error", err)
		return
	}

	// 用一份「只含这些路径」的伪快照驱动 applier，复用它的全部保障。
	sub := protocol.ConfigSnapshot{
		ConfigSetID: st.ConfigSet, RevisionID: st.Revision, Seq: st.Seq,
	}
	content := map[string][]byte{}
	cache := blobcache.New(s.d.Dir)
	subState := &state.State{Files: map[string]state.FileState{}, Health: st.Health}

	for _, p := range paths {
		fs, inBaseline := st.Files[p]
		if !inBaseline {
			// 基线里没有 = 这是本地新增的文件，恢复即删除。
			// 交给 applier 的 delete 动作：它会先快照再删，可回滚。
			subState.Files[p] = state.FileState{Rendered: "无", Mode: 0o644}
			continue
		}
		raw, err := cache.Get(fs.Blob)
		if err != nil {
			s.log.Warn("本地缺少基线内容，跳过恢复", "path", p, "hash", fs.Blob)
			continue
		}
		content[fs.Blob] = raw
		sub.Files = append(sub.Files, protocol.FileEntry{
			Path: p, Hash: fs.Blob, Size: fs.Size, Mode: fs.Mode, Keys: fs.Keys,
		})
		// 基线状态里故意填一个对不上的 rendered，逼 applier 走 overwrite
		// ——磁盘上此刻是用户改过的内容，必须被盖掉。
		subState.Files[p] = state.FileState{Blob: fs.Blob, Rendered: "陈旧", Mode: fs.Mode, Keys: fs.Keys}
	}
	sub.Checksum = protocol.Checksum(sub.Files)

	plan, err := applier.BuildPlan(sub, subState, content, sec.Lookup)
	if err != nil {
		s.log.Error("生成恢复计划失败", "error", err)
		return
	}
	ack, _ := s.app.Apply(sub, plan, subState)
	ack.RevisionID = st.Revision

	// 恢复不改变 state 的版本信息，只需把这几条的 rendered 拉回基线值。
	s.mu.Lock()
	for _, step := range plan.Steps {
		if step.Action == protocol.ActionDelete {
			delete(s.st.Files, step.Rel)
			continue
		}
		if fs, ok := s.st.Files[step.Rel]; ok {
			fs.Rendered = step.Rendered
			s.st.Files[step.Rel] = fs
		}
	}
	_ = state.Save(s.d.Dir, s.st)
	st2, sec2, w := s.st, s.sec, s.watcher
	s.mu.Unlock()

	if w != nil && st2 != nil {
		if m, err := s.manifestOf(st2); err == nil {
			_ = w.Reload(st2, m, sec2)
		}
	}
	if err := s.d.Send(protocol.KindApplyAck, ack); err != nil {
		s.log.Warn("上报恢复回执失败", "error", err)
	}
}

// manifestOf 取当前生效的 manifest。它随快照冻结在 state 里
// ——历史版本要能被解释（spec §4.1）。
func (s *Syncer) manifestOf(st *state.State) (manifest.Manifest, error) {
	if len(st.Manifest) == 0 {
		return manifest.Default(), nil
	}
	return manifest.Parse(st.Manifest)
}
```

> 两点要在实现时补齐：
> 1. `state.State` 追加字段 `Manifest json.RawMessage \`json:"manifest,omitempty"\``（随快照冻结，watcher 与恢复都要用它展开）。同步更新 00-overview 的 `State` 契约与子计划 06 的实现。
> 2. `Syncer` 结构体追加字段 `watcher *watcher.Watcher`、`sec *secrets.File`，`Deps` 追加 `ReconcileInterval time.Duration`。`applyPending` 成功之后把 `snap.Manifest` 存进 `next.Manifest`，并调 `w.Reload`。

在 `Handle` 的 switch 里追加 `case protocol.KindDriftCommand`，解出后调 `s.onDriftCommand(cmd)`。

`agent/run.go` 里在连接建立后起 watcher：

```go
			// watcher 跟着连接的生命周期走：连接断了就停，重连时重起。
			// state.json 是跨连接的持久层，因此重起不丢基线。
			go func() {
				if err := sync.StartWatcher(ctx); err != nil {
					o.Logger.Warn("文件监视退出", "error", err)
				}
			}()
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/
git commit -m "feat: agent 侧漂移上报与恢复/忽略指令"
```

---

### Task 2: hub 侧 unified diff

**Files:**
- Modify: `go.mod`（difflib 转直接依赖）
- Create: `hub/internal/drift/diff.go`
- Test: `hub/internal/drift/diff_test.go`

**Interfaces:**
- Produces: `func UnifiedDiff(path string, base, cur []byte) string`

- [ ] **Step 1: 转直接依赖**

```bash
go get github.com/pmezard/go-difflib@v1.0.0
go mod tidy
```

- [ ] **Step 2: 写失败的测试**

Create `hub/internal/drift/diff_test.go`：

```go
package drift_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
)

func TestUnifiedDiffShowsChanges(t *testing.T) {
	got := drift.UnifiedDiff(".claude/CLAUDE.md",
		[]byte("第一行\n第二行\n第三行\n"),
		[]byte("第一行\n改过的第二行\n第三行\n"))

	require.Contains(t, got, "-第二行")
	require.Contains(t, got, "+改过的第二行")
	require.Contains(t, got, " 第一行", "要有上下文行")
	require.Contains(t, got, ".claude/CLAUDE.md")
}

func TestUnifiedDiffForAddedFile(t *testing.T) {
	got := drift.UnifiedDiff(".claude/skills/foo/SKILL.md", nil, []byte("全新内容\n"))
	require.Contains(t, got, "+全新内容")
	require.NotContains(t, got, "-")
}

func TestUnifiedDiffForDeletedFile(t *testing.T) {
	got := drift.UnifiedDiff(".claude/gone.md", []byte("要没了\n"), nil)
	require.Contains(t, got, "-要没了")
}

func TestUnifiedDiffOfIdenticalContentIsEmpty(t *testing.T) {
	require.Empty(t, drift.UnifiedDiff("a", []byte("一样\n"), []byte("一样\n")))
}

// 二进制内容不该被渲染成一堆乱码塞进库里。
func TestUnifiedDiffOfBinaryIsSummarized(t *testing.T) {
	bin := []byte{0x00, 0x01, 0x02, 0xff, 0xfe}
	got := drift.UnifiedDiff("a.bin", nil, bin)
	require.Contains(t, got, "二进制")
	require.NotContains(t, got, "\x00")
}

// diff 会进库并直接渲染到 UI，必须有上限。
func TestUnifiedDiffIsTruncated(t *testing.T) {
	var sb strings.Builder
	for i := range 200000 {
		sb.WriteString("第")
		sb.WriteString(string(rune('0' + i%10)))
		sb.WriteString("行\n")
	}
	got := drift.UnifiedDiff("big.md", nil, []byte(sb.String()))
	require.LessOrEqual(t, len(got), drift.MaxDiffBytes+200)
	require.Contains(t, got, "已截断")
}
```

- [ ] **Step 3: 实现**

Create `hub/internal/drift/diff.go`：

```go
// Package drift 是收件箱：漂移落库、diff 计算、收编 / 恢复 / 忽略。
//
// diff 由 hub 算而不是 agent（spec §8.2）：agent 更瘦，算法只有一份实现，
// 而且收编因此退化成纯 hub 侧操作——用已有的 blob 组装新 Revision 就完事。
package drift

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"
)

// MaxDiffBytes 是存进 drift_events.diff 的上限。
// 它会被前端直接渲染，不设上限的话一个大文件就能让收件箱卡住。
const MaxDiffBytes = 256 << 10

// UnifiedDiff 算一份 unified diff。内容相同时返回空串。
func UnifiedDiff(path string, base, cur []byte) string {
	if string(base) == string(cur) {
		return ""
	}
	if !isText(base) || !isText(cur) {
		return fmt.Sprintf("（二进制内容，不显示差异：%s，%d → %d 字节）",
			path, len(base), len(cur))
	}

	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(base)),
		B:        difflib.SplitLines(string(cur)),
		FromFile: path + "（基线）",
		ToFile:   path + "（本机）",
		Context:  3,
	})
	if err != nil {
		return fmt.Sprintf("（计算差异失败：%v）", err)
	}
	if len(out) > MaxDiffBytes {
		return out[:MaxDiffBytes] + "\n…（差异过大，已截断）\n"
	}
	return out
}

// isText 判断内容是否适合按文本 diff。受管的都是配置文件，
// 出现二进制多半意味着有人往 skills 目录里放了别的东西。
func isText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	if !utf8.Valid(b) {
		return false
	}
	return !strings.ContainsRune(string(b), 0)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/drift/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add go.mod go.sum hub/internal/drift/
git commit -m "feat: hub 侧 unified diff 计算"
```

---

### Task 3: 漂移落库

**Files:**
- Create: `hub/internal/drift/service.go`
- Test: `hub/internal/drift/service_test.go`

**Interfaces:**
- Produces: `Deps` / `Service` / `NewService` / `HandleReport` / `IgnorePaths`（`Adopt` / `Restore` / `Ignore` / `Supersede` 在子计划 14）

**upsert 语义（spec §4.1）**：`(machine, path)` 的部分唯一索引条件是 `state = 'open'`。同一路径同一时刻只能有一条待处理漂移——**重复检测到就更新那条，不新建**。

**`Full` 报告的额外语义**：survey 的全量对账带 `Full=true`，此时**没有出现在报告里的 open 漂移要被关掉**（说明它已经不再漂移了）。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/drift/service_test.go`：

```go
package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestHandleReportCreatesOpenEvents(t *testing.T) {
	r := newRig(t) // 见下方说明：与 configsync 的 rig 同构，另带 drift.Service
	r.assign(t)

	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{
			{Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified,
				BaseHash: r.hashOf(t, ".claude/CLAUDE.md"), Content: []byte("改过了\n"), Mode: 0o644},
			{Path: ".claude/skills/foo/SKILL.md", Kind: protocol.DriftAdded,
				Content: []byte("新技能\n"), Mode: 0o644},
		},
		Final: true,
	}))

	recs := r.drifts(t)
	require.Len(t, recs, 2)
	for _, rec := range recs {
		require.Equal(t, "open", rec.GetString("state"))
		require.Equal(t, r.setID, rec.GetString("config_set"))
		require.NotEmpty(t, rec.GetString("current_blob"), "内容要落 blob")
	}

	byPath := r.driftsByPath(t)
	require.Contains(t, byPath[".claude/CLAUDE.md"].GetString("diff"), "+改过了")
	require.Equal(t, "added", byPath[".claude/skills/foo/SKILL.md"].GetString("kind"))
	require.Empty(t, byPath[".claude/skills/foo/SKILL.md"].GetString("base_hash"))

	r.requireEvent(t, events.KindDriftReported)
}

// 同一路径重复上报：更新那条，不新建（部分唯一索引的语义）。
func TestHandleReportUpsertsSamePath(t *testing.T) {
	r := newRig(t)
	r.assign(t)

	for _, body := range []string{"第一次改\n", "第二次改\n"} {
		require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
			Items: []protocol.DriftItem{{
				Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified,
				Content: []byte(body), Mode: 0o644,
			}},
			Final: true,
		}))
	}

	recs := r.drifts(t)
	require.Len(t, recs, 1, "同一路径只该有一条 open 漂移")
	require.Contains(t, recs[0].GetString("diff"), "+第二次改")
}

// 已解决的漂移不占位：同一路径可以再开新的。
func TestHandleReportCreatesNewEventAfterResolution(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{Path: "a", Kind: protocol.DriftModified, Content: []byte("一\n")}},
		Final: true,
	}))
	r.resolve(t, "a", "adopted")

	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{Path: "a", Kind: protocol.DriftModified, Content: []byte("二\n")}},
		Final: true,
	}))
	require.Len(t, r.drifts(t), 2)
	require.Len(t, r.openDrifts(t), 1)
}

// Truncated 的条目只记路径，不落 blob（内容根本没传上来）。
func TestHandleReportTruncatedItem(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{
			Path: ".claude/settings.json", Kind: protocol.DriftModified, Truncated: true,
		}},
		Final: true,
	}))
	rec := r.openDrifts(t)[0]
	require.True(t, rec.GetBool("truncated"))
	require.Empty(t, rec.GetString("current_blob"))
	require.Contains(t, rec.GetString("diff"), "未能安全脱敏")
}

func TestHandleReportRestorePartialIsRecorded(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{
			Path: "a", Kind: protocol.DriftModified,
			Content: []byte("main 与 {{var.ws}}\n"), RestorePartial: true,
		}},
		Final: true,
	}))
	require.True(t, r.openDrifts(t)[0].GetBool("restore_partial"))
}

// 全量对账：没出现在报告里的 open 漂移说明已经不漂了，要关掉。
func TestFullReportClosesVanishedDrifts(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{
			{Path: "a", Kind: protocol.DriftModified, Content: []byte("一\n")},
			{Path: "b", Kind: protocol.DriftModified, Content: []byte("二\n")},
		},
		Final: true,
	}))
	require.Len(t, r.openDrifts(t), 2)

	// 用户手工把 a 改回去了，下一次全量对账里只剩 b
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{Path: "b", Kind: protocol.DriftModified, Content: []byte("二\n")}},
		Final: true, Full: true,
	}))

	open := r.openDrifts(t)
	require.Len(t, open, 1)
	require.Equal(t, "b", open[0].GetString("path"))
}

// 增量报告不关任何东西——它只知道自己看见的那几条。
func TestIncrementalReportDoesNotCloseOthers(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{
			{Path: "a", Kind: protocol.DriftModified, Content: []byte("一\n")},
			{Path: "b", Kind: protocol.DriftModified, Content: []byte("二\n")},
		},
		Final: true, Full: true,
	}))

	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{Path: "b", Kind: protocol.DriftModified, Content: []byte("改了\n")}},
		Final: true, // Full 为 false
	}))
	require.Len(t, r.openDrifts(t), 2)
}

// 分批到达：只有 Final 那批到了才做全量关闭判定。
func TestFullReportAcrossBatches(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{Path: "a", Kind: protocol.DriftModified, Content: []byte("一\n")}},
		Full:  true, // 非最后一批
	}))
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{Path: "b", Kind: protocol.DriftModified, Content: []byte("二\n")}},
		Full:  true, Final: true,
	}))
	require.Len(t, r.openDrifts(t), 2, "两批都要保留")
}

func TestIgnorePathsMergesGlobalAndMachineRules(t *testing.T) {
	r := newRig(t)
	r.addIgnoreRule(t, "", "**/.DS_Store")
	r.addIgnoreRule(t, r.machineID, ".claude/skills/scratch/**")
	r.addIgnoreRule(t, r.otherMachineID(t), ".claude/别人的/**")

	got, err := r.svc.IgnorePaths(r.machineID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"**/.DS_Store", ".claude/skills/scratch/**"}, got)
}

// 未指派配置集的机器上报漂移：记日志，不落库（没有配置集就无从收编）。
func TestHandleReportOfUnassignedMachineIsIgnored(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.HandleReport(r.machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{Path: "a", Kind: protocol.DriftModified, Content: []byte("x")}},
		Final: true,
	}))
	require.Empty(t, r.drifts(t))
}
```

`newRig` 与它的辅助方法（`assign` / `drifts` / `openDrifts` / `driftsByPath` / `hashOf` / `resolve` / `addIgnoreRule` / `otherMachineID` / `requireEvent`）建在 `hub/internal/drift/rig_test.go` 里，结构照抄 `hub/internal/configsync/service_test.go` 的 `rig`，另加 `drift.Service` 与一个已发布并指派好的配置集。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/drift/...`
Expected: FAIL，`undefined: drift.NewService`

- [ ] **Step 3: 实现**

Create `hub/internal/drift/service.go`。要点：

```go
// HandleReport 落一批漂移上报（spec §8.2）。
//
// 同一路径同一时刻只能有一条待处理漂移，由 (machine, path) where state='open'
// 的部分唯一索引强制。重复检测到就更新那条，不新建（spec §4.1）。
func (s *Service) HandleReport(machineID string, rep protocol.DriftReport) error {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		// 没有配置集就无从收编。这不是错误——机器可能刚被解除指派。
		s.log.Debug("机器未指派配置集，忽略漂移上报", "machine", machineID)
		return nil
	}
	setID := assign.GetString("config_set")

	baseFiles, err := s.baselineFiles(setID)
	if err != nil {
		return err
	}

	seen := make([]string, 0, len(rep.Items))
	for _, it := range rep.Items {
		if err := s.upsert(machineID, setID, it, baseFiles); err != nil {
			s.log.Warn("落库漂移失败", "machine", machineID, "path", it.Path, "error", err)
			continue
		}
		seen = append(seen, it.Path)
	}

	// Full 的最后一批到齐后，把没出现在报告里的 open 漂移关掉——
	// 说明用户已经手工把它们改回去了。增量报告不做这件事：它只知道
	// 自己看见的那几条。
	if rep.Full && rep.Final {
		if err := s.closeVanished(machineID, s.fullSeen(machineID, seen)); err != nil {
			return err
		}
	}
	if len(seen) > 0 {
		if err := s.d.Events.Write(events.KindDriftReported, machineID, map[string]any{
			"count": len(seen), "full": rep.Full,
		}); err != nil {
			s.log.Warn("写 drift.reported 事件失败", "error", err)
		}
	}
	return nil
}
```

`fullSeen` 跨批累积：`Service` 里存一张 `map[string][]string`（machineID → 本轮全量对账已见路径），`Final` 到达时取出并清空。

`upsert` 的要点：

- `Truncated` 的条目不落 blob，`diff` 填一句提示文案（「该文件含未能安全脱敏的凭据，请在 Web 上手工处理」，spec §6.4）。
- 其余条目 `blobs.Put(it.Content)`，`current_blob` 指向那条记录。
- `diff` 由 `UnifiedDiff(path, baseContent, it.Content)` 算出：`baseContent` 从 head revision 的清单里按 path 找到 hash 再 `blobs.Get`；`added` 类的 base 为 nil，`deleted` 类的 cur 为 nil。
- 查已有 open 记录：`FindRecordsByFilter("drift_events", "machine = {:m} && path = {:p} && state = 'open'", ...)`。

`IgnorePaths` 与 `configsync.ignorePaths` 同查询，抽到 drift 包并让 configsync 调它（避免两份实现）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/drift/...`
Expected: PASS

- [ ] **Step 5: 接线**

`configsync.Deps` 追加 `Drift interface{ HandleReport(machineID string, rep protocol.DriftReport) error }`，`configsync.Service.DriftReport` 转交给它；`hub.go` 里构造 `drift.NewService` 并塞进去。注意构造顺序：`drift` 需要 `configsync`（发 `DriftCommand`），`configsync` 需要 `drift`（转交上报）——用「先建 configsync（Drift 字段留空），再建 drift，最后 `sync.SetDrift(d)`」的两步装配。

- [ ] **Step 6: 集成测试**

Create `internal/testsupport/drift_test.go`：

```go
//go:build testing

// DoD 第 4、5、6 条的前半：改文件 → 60 秒内进收件箱，内容已脱敏。
func TestDriftReachesInbox(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())

	th.SeedCredential(t, "k", "sk-real-secret-9999")
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md":     "# 原始规矩\n",
		".claude/settings.json": `{"K":"{{cred.k}}"}`,
	})
	ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)

	// 改 CLAUDE.md 与含密钥的 settings.json
	ta.WriteManaged(t, ".claude/CLAUDE.md", []byte("# 改过的规矩\n"), 0o644)
	ta.WriteManaged(t, ".claude/settings.json",
		[]byte(`{"K":"sk-real-secret-9999","new":true}`), 0o600)
	ta.TriggerReconcile(t)

	md := th.RequireDrift(t, ta.MachineID, ".claude/CLAUDE.md")
	require.Equal(t, "modified", md.GetString("kind"))
	require.Contains(t, md.GetString("diff"), "+# 改过的规矩")

	th.RequireDrift(t, ta.MachineID, ".claude/settings.json")
	// DoD 第 6 条：上报的内容里不含明文
	th.RequireNoPlaintextInBlobs(t, "sk-real-secret-9999")
}

// tree 模式的新增文件要能进收件箱（DoD 第 4 条的前半）。
func TestNewSkillReachesInbox(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/skills/foo/SKILL.md": "旧技能\n",
	})
	ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)

	ta.WriteManaged(t, ".claude/skills/新的/SKILL.md", []byte("# 新技能\n"), 0o644)
	ta.TriggerReconcile(t)

	rec := th.RequireDrift(t, ta.MachineID, ".claude/skills/新的/SKILL.md")
	require.Equal(t, "added", rec.GetString("kind"))
}
```

`TestAgent.TriggerReconcile` 与 `TestHub.RequireDrift` 加进 testsupport：前者调 `syncer.ReconcileNow`（因此 `RunSync` 要把 syncer 存进 `TestAgent`），后者用 `require.Eventually` 轮询 `drift_events`。

- [ ] **Step 7: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add hub/ agent/ internal/testsupport/
git commit -m "feat: 漂移上报落库与收件箱写入"
```
