# 子计划 14 · 收件箱：收编、恢复、忽略、冲突、superseded

**前置**：04、13
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`drift.Service` 的 `Adopt` / `Restore` / `Ignore` / `Supersede`、跨机器同路径冲突的**硬阻止**、apply 撞上未解决漂移时的 `superseded` 处置，以及 `hub/api.go` 与路由的对应入口。

**收编流程（spec §8.3）**

1. 选中 N 条 open drift（可跨文件；可跨机器，但必须同一配置集）
2. 以 head Revision 为基，逐条把 `current_blob` 覆盖进清单（`added` → 新增条目，`deleted` → 移除条目）
3. 生成新 Revision（`source=adopt`），note 自动填「收编自 `<machine>` 的 N 项改动」
4. 这些 drift_event 置 `adopted`，记 `resolved_revision`
5. 向指派该配置集的**所有**机器发 `ConfigNotify`，**包括来源机器**

**第 5 步不给来源机器开特例**（取舍记录 #10）：它 apply 后 rendered hash 与磁盘一致，plan 全是 skip，天然幂等。开特例就多一条会腐烂的分支。

---

### Task 1: 收编

**Files:**
- Create: `hub/internal/drift/adopt.go`
- Test: `hub/internal/drift/adopt_test.go`

**Interfaces:**
- Produces:
  ```go
  var ErrConflict = errors.New("drift: 多台机器改了同一路径，必须先做三方对比")
  var ErrMixedConfigSets = errors.New("drift: 选中的漂移不属于同一个配置集")
  var ErrNeedsReview = errors.New("drift: 含未完全还原的变量，需先人工确认")

  func (s *Service) Adopt(eventIDs []string) (*core.Record, error)
  func (s *Service) AdoptReviewed(eventIDs []string, reviewed []string) (*core.Record, error)
  ```

`AdoptReviewed` 的 `reviewed` 是用户已逐条确认过的 event id 集合——含 `restore_partial` 的条目必须在里面，否则返回 `ErrNeedsReview`（spec §8.3）。`Adopt` 等价于 `AdoptReviewed(ids, nil)`。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/drift/adopt_test.go`：

```go
package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestAdoptCreatesRevisionFromCurrentBlobs(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified,
		Content: []byte("# 改过的规矩\n"), Mode: 0o644,
	})

	id := r.openDrifts(t)[0].Id
	rev, err := r.svc.Adopt([]string{id})
	require.NoError(t, err)
	require.Equal(t, "adopt", rev.GetString("source"))
	require.EqualValues(t, 2, rev.GetInt("seq"), "收编生成新版本")
	require.Contains(t, rev.GetString("note"), "收编自")

	files, err := r.revs.Files(rev.Id)
	require.NoError(t, err)
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Hash
	}
	content, err := r.blobs.Get(byPath[".claude/CLAUDE.md"])
	require.NoError(t, err)
	require.Equal(t, "# 改过的规矩\n", string(content))

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "adopted", rec.GetString("state"))
	require.Equal(t, rev.Id, rec.GetString("resolved_revision"))
	require.NotEmpty(t, rec.GetString("resolved_at"))

	r.requireEvent(t, events.KindDriftAdopted)
}

// added → 清单里新增条目。
func TestAdoptAddsNewFile(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/skills/新的/SKILL.md", Kind: protocol.DriftAdded,
		Content: []byte("# 新技能\n"), Mode: 0o644,
	})

	rev, err := r.svc.Adopt([]string{r.openDrifts(t)[0].Id})
	require.NoError(t, err)
	files, err := r.revs.Files(rev.Id)
	require.NoError(t, err)

	var found bool
	for _, f := range files {
		if f.Path == ".claude/skills/新的/SKILL.md" {
			found = true
		}
	}
	require.True(t, found, "added 的漂移要在新版本里新增条目")
}

// deleted → 从清单里移除条目。
func TestAdoptRemovesDeletedFile(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftDeleted,
		BaseHash: r.hashOf(t, ".claude/CLAUDE.md"),
	})

	rev, err := r.svc.Adopt([]string{r.openDrifts(t)[0].Id})
	require.NoError(t, err)
	files, err := r.revs.Files(rev.Id)
	require.NoError(t, err)
	for _, f := range files {
		require.NotEqual(t, ".claude/CLAUDE.md", f.Path)
	}
}

// 多条一次收编，生成一个版本而不是三个。
func TestAdoptMultipleInOneRevision(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t,
		protocol.DriftItem{Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Content: []byte("一\n")},
		protocol.DriftItem{Path: ".claude/settings.json", Kind: protocol.DriftModified, Content: []byte(`{"a":1}`)},
	)
	var ids []string
	for _, rec := range r.openDrifts(t) {
		ids = append(ids, rec.Id)
	}

	rev, err := r.svc.Adopt(ids)
	require.NoError(t, err)
	require.EqualValues(t, 2, rev.GetInt("seq"), "两条漂移只生成一个新版本")
	require.Empty(t, r.openDrifts(t))
}

// 跨机器同路径冲突：硬阻止，不是警告（取舍记录 #9）。
func TestAdoptRejectsCrossMachineConflict(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	other := r.secondMachine(t)

	r.reportAs(t, r.machineID, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Content: []byte("A 的改动\n"),
	})
	r.reportAs(t, other, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Content: []byte("B 的改动\n"),
	})

	var ids []string
	for _, rec := range r.openDrifts(t) {
		ids = append(ids, rec.Id)
	}
	require.Len(t, ids, 2)

	_, err := r.svc.Adopt(ids)
	require.ErrorIs(t, err, drift.ErrConflict)
	require.Len(t, r.openDrifts(t), 2, "冲突时不该动任何记录")

	// 只选一条则可以
	_, err = r.svc.Adopt(ids[:1])
	require.NoError(t, err)
}

// 跨配置集不允许：一个 Revision 只属于一个配置集。
func TestAdoptRejectsMixedConfigSets(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	otherMachine, otherSet := r.secondMachineWithOwnSet(t)

	r.reportAs(t, r.machineID, protocol.DriftItem{
		Path: "a", Kind: protocol.DriftModified, Content: []byte("一\n")})
	r.reportAs(t, otherMachine, protocol.DriftItem{
		Path: "b", Kind: protocol.DriftModified, Content: []byte("二\n")})
	_ = otherSet

	var ids []string
	for _, rec := range r.openDrifts(t) {
		ids = append(ids, rec.Id)
	}
	_, err := r.svc.Adopt(ids)
	require.ErrorIs(t, err, drift.ErrMixedConfigSets)
}

// restore_partial 的条目要先人工确认（spec §6.4 / §8.3）。
func TestAdoptRequiresReviewForPartialRestore(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: "a", Kind: protocol.DriftModified,
		Content: []byte("main 与 {{var.ws}}\n"), RestorePartial: true,
	})
	id := r.openDrifts(t)[0].Id

	_, err := r.svc.Adopt([]string{id})
	require.ErrorIs(t, err, drift.ErrNeedsReview)

	rev, err := r.svc.AdoptReviewed([]string{id}, []string{id})
	require.NoError(t, err)
	require.NotEmpty(t, rev.Id)
}

// truncated 的条目没有内容，收编不了。
func TestAdoptRejectsTruncated(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified, Truncated: true,
	})
	_, err := r.svc.Adopt([]string{r.openDrifts(t)[0].Id})
	require.ErrorContains(t, err, "未能安全脱敏")
}

// 收编后通知全部指派机器，**包括来源机器**（取舍记录 #10）。
func TestAdoptNotifiesAllAssignedMachinesIncludingSource(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	other := r.secondMachine(t)

	r.report(t, protocol.DriftItem{
		Path: "a", Kind: protocol.DriftModified, Content: []byte("改了\n")})
	_, err := r.svc.Adopt([]string{r.openDrifts(t)[0].Id})
	require.NoError(t, err)

	var notified []string
	for _, m := range r.sender.of(protocol.KindConfigNotify) {
		notified = append(notified, m.machine)
	}
	require.Contains(t, notified, r.machineID, "来源机器不开特例——幂等保证它是空操作")
	require.Contains(t, notified, other)
}
```

`rig` 需要补的辅助：`report` / `reportAs`（走 `HandleReport`）、`secondMachine`、`secondMachineWithOwnSet`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/drift/ -run Adopt -v`
Expected: FAIL，`s.Adopt undefined`

- [ ] **Step 3: 实现**

Create `hub/internal/drift/adopt.go`：

```go
package drift

import (
	"errors"
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

var (
	// ErrConflict：两台机器对同一文件的改动，静默取其一是最容易让人丢
	// 工作成果的操作。产品 §4.4 只要求「显式警告」，这里做成硬阻止
	// （取舍记录 #9）。
	ErrConflict = errors.New("drift: 多台机器改了同一路径，必须先做三方对比")

	ErrMixedConfigSets = errors.New("drift: 选中的漂移不属于同一个配置集")

	// ErrNeedsReview：含未完全还原的变量，diff 里会显示 main vs
	// {{var.workspace}}，用户看得懂，但必须先看一眼（spec §6.4）。
	ErrNeedsReview = errors.New("drift: 含未完全还原的变量，需先人工确认")
)

func (s *Service) Adopt(eventIDs []string) (*core.Record, error) {
	return s.AdoptReviewed(eventIDs, nil)
}

// AdoptReviewed 把选中的漂移合进一个新 Revision（spec §8.3）。
//
// reviewed 是用户已逐条确认过的 event id；含 restore_partial 的条目必须
// 出现在里面。
func (s *Service) AdoptReviewed(eventIDs, reviewed []string) (*core.Record, error) {
	if len(eventIDs) == 0 {
		return nil, fmt.Errorf("drift: 没有选中任何漂移")
	}
	reviewedSet := map[string]bool{}
	for _, id := range reviewed {
		reviewedSet[id] = true
	}

	recs := make([]*core.Record, 0, len(eventIDs))
	setID := ""
	seenPath := map[string]string{} // path → machineID
	machines := map[string]bool{}

	for _, id := range eventIDs {
		rec, err := s.d.App.FindRecordById("drift_events", id)
		if err != nil {
			return nil, fmt.Errorf("drift: 漂移 %s 不存在: %w", id, err)
		}
		if rec.GetString("state") != "open" {
			return nil, fmt.Errorf("drift: 漂移 %s 已被处理过", id)
		}
		if rec.GetBool("truncated") {
			return nil, fmt.Errorf("drift: %s 未能安全脱敏，无法收编；"+
				"请在 Web 上手工处理该文件", rec.GetString("path"))
		}
		if rec.GetBool("restore_partial") && !reviewedSet[id] {
			return nil, fmt.Errorf("%w: %s", ErrNeedsReview, rec.GetString("path"))
		}

		cur := rec.GetString("config_set")
		if setID == "" {
			setID = cur
		} else if setID != cur {
			return nil, ErrMixedConfigSets
		}

		path := rec.GetString("path")
		machine := rec.GetString("machine")
		// 冲突检查在**任何写入之前**完成：要么整批成，要么什么都不动。
		if other, dup := seenPath[path]; dup && other != machine {
			return nil, fmt.Errorf("%w: %s", ErrConflict, path)
		}
		seenPath[path] = machine
		machines[machine] = true

		recs = append(recs, rec)
	}

	// 以 head Revision 为基，逐条覆盖。
	head, err := s.d.Revs.Head(setID)
	if err != nil {
		return nil, err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return nil, err
	}
	byPath := map[string]protocol.FileEntry{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	for _, rec := range recs {
		path := rec.GetString("path")
		switch rec.GetString("kind") {
		case "deleted":
			delete(byPath, path)
		default:
			blobRec, err := s.d.App.FindRecordById("blobs", rec.GetString("current_blob"))
			if err != nil {
				return nil, fmt.Errorf("drift: %s 的内容不在库里: %w", path, err)
			}
			hash := blobRec.GetString("hash")
			content, err := s.d.Blobs.Get(hash)
			if err != nil {
				return nil, err
			}
			mode := uint32(rec.GetInt("mode"))
			if mode == 0 {
				mode = 0o644
			}
			byPath[path] = protocol.FileEntry{
				Path: path, Hash: hash, Size: uint32(len(content)),
				Mode: mode, Keys: byPath[path].Keys,
			}
		}
	}

	merged := make([]protocol.FileEntry, 0, len(byPath))
	for _, f := range byPath {
		merged = append(merged, f)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Path < merged[j].Path })

	note := adoptNote(s.d.App, machines, len(recs))
	rev, err := s.d.Revs.PublishFiles(setID, merged, note, "adopt")
	if err != nil {
		return nil, err
	}

	now := types.NowDateTime()
	for _, rec := range recs {
		rec.Set("state", "adopted")
		rec.Set("resolved_revision", rev.Id)
		rec.Set("resolved_at", now)
		if err := s.d.App.Save(rec); err != nil {
			s.log.Warn("标记漂移为已收编失败", "id", rec.Id, "error", err)
		}
		if err := s.d.Events.Write(events.KindDriftAdopted, rec.GetString("machine"),
			map[string]any{"path": rec.GetString("path"), "revision": rev.Id}); err != nil {
			s.log.Warn("写 drift.adopted 事件失败", "error", err)
		}
	}

	// 通知**所有**指派机器，包括来源机器：它 apply 后 rendered hash 与磁盘
	// 一致，plan 全是 skip，天然幂等。不开特例就少一条会腐烂的分支
	// （取舍记录 #10）。
	if err := s.d.Sync.NotifyConfigSet(setID, rev.Id, protocol.ReasonAdopted); err != nil {
		s.log.Warn("收编后通知失败", "config_set", setID, "error", err)
	}
	return rev, nil
}

func adoptNote(app core.App, machines map[string]bool, n int) string {
	names := make([]string, 0, len(machines))
	for id := range machines {
		name := id
		if r, err := app.FindRecordById("machines", id); err == nil {
			if v := r.GetString("name"); v != "" {
				name = v
			} else if v := r.GetString("hostname"); v != "" {
				name = v
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 1 {
		return fmt.Sprintf("收编自 %s 的 %d 项改动", names[0], n)
	}
	return fmt.Sprintf("收编自 %d 台机器的 %d 项改动", len(names), n)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/drift/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/drift/
git commit -m "feat: 漂移收编与跨机器冲突硬阻止"
```

---

### Task 2: 恢复、忽略与 superseded

**Files:**
- Create: `hub/internal/drift/resolve.go`
- Test: `hub/internal/drift/resolve_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s *Service) Restore(eventIDs []string) error
  func (s *Service) Ignore(eventIDs []string, global bool) error
  func (s *Service) Supersede(machineID string, paths []string, revID string) error
  func (s *Service) MarkRestored(machineID string, paths []string) error
  ```

**`superseded`（spec §7.7）**：apply 撞上未解决的漂移时**照常覆盖**（决策 C1 中台为准），但绝不静默丢数据——该路径的当前内容已经作为 `current_blob` 存在 hub 里，apply 时把对应 drift_event 置为 `superseded` 而非删除。用户在「已被覆盖」筛选里仍能看到它、看 diff、并重新收编。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/drift/resolve_test.go`：

```go
package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestRestoreSendsDriftCommand(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Content: []byte("改了\n")})

	require.NoError(t, r.svc.Restore([]string{r.openDrifts(t)[0].Id}))

	sent := r.sender.of(protocol.KindDriftCommand)
	require.Len(t, sent, 1)
	cmd := sent[0].payload.(protocol.DriftCommand)
	require.Equal(t, protocol.OpRestore, cmd.Op)
	require.Equal(t, []string{".claude/CLAUDE.md"}, cmd.Paths)

	// 状态在 agent 回执之后才变，不能提前置 restored
	require.Len(t, r.openDrifts(t), 1, "指令发出不等于恢复成功")
}

func TestMarkRestoredClosesEvent(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Content: []byte("改了\n")})
	id := r.openDrifts(t)[0].Id
	require.NoError(t, r.svc.Restore([]string{id}))

	require.NoError(t, r.svc.MarkRestored(r.machineID, []string{".claude/CLAUDE.md"}))

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "restored", rec.GetString("state"))
	require.NotEmpty(t, rec.GetString("resolved_at"))
	r.requireEvent(t, events.KindDriftRestored)
}

// 跨机器一次恢复：每台机器各发一条指令。
func TestRestoreGroupsByMachine(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	other := r.secondMachine(t)
	r.reportAs(t, r.machineID, protocol.DriftItem{Path: "a", Kind: protocol.DriftModified, Content: []byte("一")})
	r.reportAs(t, other, protocol.DriftItem{Path: "b", Kind: protocol.DriftModified, Content: []byte("二")})

	var ids []string
	for _, rec := range r.openDrifts(t) {
		ids = append(ids, rec.Id)
	}
	require.NoError(t, r.svc.Restore(ids))
	require.Len(t, r.sender.of(protocol.KindDriftCommand), 2)
}

func TestIgnoreWritesMachineRule(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/skills/scratch/tmp.md", Kind: protocol.DriftAdded, Content: []byte("临时\n")})
	id := r.openDrifts(t)[0].Id

	require.NoError(t, r.svc.Ignore([]string{id}, false))

	rules, err := r.app.FindAllRecords("ignore_rules")
	require.NoError(t, err)
	require.Len(t, rules, 1)
	require.Equal(t, r.machineID, rules[0].GetString("machine"))
	require.Equal(t, ".claude/skills/scratch/tmp.md", rules[0].GetString("path"))

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "ignored", rec.GetString("state"))
	r.requireEvent(t, events.KindDriftIgnored)

	// 同时给 agent 下指令，免得等到下一次快照才生效
	sent := r.sender.of(protocol.KindDriftCommand)
	require.Len(t, sent, 1)
	require.Equal(t, protocol.OpIgnore, sent[0].payload.(protocol.DriftCommand).Op)
}

func TestIgnoreGlobalLeavesMachineEmpty(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{Path: "x/.DS_Store", Kind: protocol.DriftAdded, Content: []byte("x")})
	require.NoError(t, r.svc.Ignore([]string{r.openDrifts(t)[0].Id}, true))

	rules, err := r.app.FindAllRecords("ignore_rules")
	require.NoError(t, err)
	require.Empty(t, rules[0].GetString("machine"), "全局规则的 machine 为空")
}

// apply 覆盖了未解决的漂移：置 superseded，不删除（spec §7.7）。
func TestSupersedeKeepsRecordAndBlob(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Content: []byte("本地改动\n")})
	id := r.openDrifts(t)[0].Id
	blobID := r.openDrifts(t)[0].GetString("current_blob")

	require.NoError(t, r.svc.Supersede(r.machineID, []string{".claude/CLAUDE.md"}, "rev-new"))

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "superseded", rec.GetString("state"))
	require.Equal(t, blobID, rec.GetString("current_blob"),
		"内容要留着——用户还能在「已被覆盖」筛选里把它捞回来")
	require.Equal(t, "rev-new", rec.GetString("resolved_revision"))
	r.requireEvent(t, events.KindDriftSuperseded)

	// blob 本身没被删
	_, err = r.app.FindRecordById("blobs", blobID)
	require.NoError(t, err)
}

// 已解决的漂移不受 supersede 影响。
func TestSupersedeOnlyTouchesOpen(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.report(t, protocol.DriftItem{Path: "a", Kind: protocol.DriftModified, Content: []byte("x")})
	id := r.openDrifts(t)[0].Id
	require.NoError(t, r.svc.Ignore([]string{id}, false))

	require.NoError(t, r.svc.Supersede(r.machineID, []string{"a"}, "rev-new"))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "ignored", rec.GetString("state"), "已忽略的不该被改成 superseded")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/drift/ -run 'Restore|Ignore|Supersede|MarkRestored' -v`
Expected: FAIL，`s.Restore undefined`

- [ ] **Step 3: 实现**

Create `hub/internal/drift/resolve.go`。要点：

- `Restore` 按机器分组，各发一条 `DriftCommand{Op: restore, Paths}`；**不立刻改状态**——恢复是否成功要等 agent 的 `ApplyAck`。
- `MarkRestored` 由 `configsync.ApplyAck` 的处理路径调用（`ack.OK && ack.RevisionID` 与当前基线相同时，把这批路径的 open 漂移置 `restored`）。
- `Ignore` 写 `ignore_rules`（`global` 为真时 `machine` 留空）、置 `ignored`、并给 agent 发一条 `DriftCommand{Op: ignore}` 让它立即生效，不必等下一份快照。
- `Supersede` 只动 `state = 'open'` 的记录。

- [ ] **Step 4: 接线 supersede 与 restored**

`configsync.ApplyAck` 里，成功的回执要做两件额外的事：

```go
	case a.OK:
		// ...原有的状态更新...
		// apply 覆盖过的路径若有 open 漂移，置 superseded 而非删除
		// ——内容在 blob 里追得回来（spec §7.7）。
		var overwritten []string
		for _, res := range a.Results {
			if res.Action == protocol.ActionOverwrite || res.Action == protocol.ActionDelete ||
				res.Action == protocol.ActionMerge {
				overwritten = append(overwritten, res.Path)
			}
		}
		if s.d.Drift != nil && len(overwritten) > 0 {
			if err := s.d.Drift.Supersede(machineID, overwritten, a.RevisionID); err != nil {
				s.log.Warn("标记被覆盖的漂移失败", "machine", machineID, "error", err)
			}
		}
```

`configsync.Deps.Drift` 的接口相应扩成：

```go
	Drift interface {
		HandleReport(machineID string, rep protocol.DriftReport) error
		Supersede(machineID string, paths []string, revID string) error
		MarkRestored(machineID string, paths []string) error
	}
```

恢复的回执与普通 apply 回执长得一样（都是 `ApplyAck`）。区分办法：**恢复回执里的 `Results` 全部落在有 open 漂移的路径上**。实现上更简单也更可靠的做法是让 `MarkRestored` 幂等——把这批路径里状态为 `open` 且 `resolved_revision` 为空的置 `restored`，其余不动，并在 `Supersede` 之后调用（顺序要紧：先 supersede 再 markRestored 会把被覆盖的也标成 restored）。因此在 `ApplyAck` 里的顺序是：

1. 先 `MarkRestored`（只认「刚下过 restore 指令」的路径——`Service` 里存一张 `pendingRestore map[string]map[string]bool`，`Restore` 时写入，`MarkRestored` 时消费）
2. 再 `Supersede`（剩下的 open 漂移都是被覆盖的）

- [ ] **Step 5: 跑测试确认通过**

Run: `go test -tags=testing ./hub/...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add hub/
git commit -m "feat: 漂移恢复、忽略与被覆盖处置"
```

---

### Task 3: API 入口与端到端测试

**Files:**
- Modify: `hub/api.go`、`hub/internal/routes/config.go`
- Create: `internal/testsupport/inbox_test.go`

**Interfaces:**
- Produces:
  ```go
  func (h *Hub) AdoptDrift(eventIDs []string) (string, error)
  func (h *Hub) AdoptDriftReviewed(eventIDs, reviewed []string) (string, error)
  func (h *Hub) RestoreDrift(eventIDs []string) error
  func (h *Hub) IgnoreDrift(eventIDs []string, global bool) error
  ```

路由：`POST /api/orciny/drift/adopt`（body 含 `events` 与可选 `reviewed`）、`/restore`、`/ignore`。`ErrConflict` → 409 并在响应里带上冲突路径；`ErrNeedsReview` → 409 并带上需要复核的 event id。

- [ ] **Step 1: 写端到端测试**

Create `internal/testsupport/inbox_test.go`：

```go
//go:build testing

// DoD 第 4 条：新增 skill → 进收件箱 → 收编 → 其余机器自动落盘。
func TestAdoptPropagatesToOtherMachines(t *testing.T) {
	th := testsupport.NewTestHub(t)
	a := testsupport.NewTestAgent(t, th, t.TempDir())
	b := testsupport.NewTestAgent(t, th, t.TempDir())

	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md": "# 规矩\n",
	})
	a.RunSync(t, th)
	b.RunSync(t, th)
	th.Assign(t, a.MachineID, setID, configsets.ModeApply)
	th.Assign(t, b.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, a.MachineID, configsets.StateAligned)
	th.RequireAssignmentState(t, b.MachineID, configsets.StateAligned)

	// 在 A 上新写一个 skill
	a.WriteManaged(t, ".claude/skills/新的/SKILL.md", []byte("# 新技能\n"), 0o644)
	a.TriggerReconcile(t)
	rec := th.RequireDrift(t, a.MachineID, ".claude/skills/新的/SKILL.md")

	// 收编
	revID, err := th.Hub.AdoptDrift([]string{rec.Id})
	require.NoError(t, err)
	require.NotEmpty(t, revID)

	// B 自动落盘
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(b.ManagedHome, ".claude/skills/新的/SKILL.md"))
		return err == nil
	}, 3*time.Second, 20*time.Millisecond, "其余机器未自动对齐")
	require.Equal(t, "# 新技能\n", string(b.ReadManaged(t, ".claude/skills/新的/SKILL.md")))

	// A 也收到通知，但它 apply 后是全 skip（幂等，取舍记录 #10）
	th.RequireAssignmentState(t, a.MachineID, configsets.StateAligned)
	require.Equal(t, "# 新技能\n", string(a.ReadManaged(t, ".claude/skills/新的/SKILL.md")))
}

// DoD 第 5 条：改 CLAUDE.md → diff 正确 → 恢复 → 文件回到基线。
func TestRestoreBringsFileBack(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md": "# 原始规矩\n",
	})
	ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)

	ta.WriteManaged(t, ".claude/CLAUDE.md", []byte("# 乱改的\n"), 0o644)
	ta.TriggerReconcile(t)
	rec := th.RequireDrift(t, ta.MachineID, ".claude/CLAUDE.md")
	require.Contains(t, rec.GetString("diff"), "+# 乱改的")

	require.NoError(t, th.Hub.RestoreDrift([]string{rec.Id}))

	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(ta.ManagedHome, ".claude/CLAUDE.md"))
		return err == nil && string(b) == "# 原始规矩\n"
	}, 3*time.Second, 20*time.Millisecond, "恢复未生效")

	require.Eventually(t, func() bool {
		r, err := th.App.FindRecordById("drift_events", rec.Id)
		return err == nil && r.GetString("state") == "restored"
	}, 3*time.Second, 20*time.Millisecond)
}

// DoD 第 11 条：两台机器对同一文件各有漂移 → 强制三方对比，不允许直接收编。
func TestCrossMachineConflictIsBlocked(t *testing.T) {
	th := testsupport.NewTestHub(t)
	a := testsupport.NewTestAgent(t, th, t.TempDir())
	b := testsupport.NewTestAgent(t, th, t.TempDir())
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{".claude/CLAUDE.md": "# 基线\n"})
	a.RunSync(t, th)
	b.RunSync(t, th)
	th.Assign(t, a.MachineID, setID, configsets.ModeApply)
	th.Assign(t, b.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, a.MachineID, configsets.StateAligned)
	th.RequireAssignmentState(t, b.MachineID, configsets.StateAligned)

	a.WriteManaged(t, ".claude/CLAUDE.md", []byte("# A 的版本\n"), 0o644)
	b.WriteManaged(t, ".claude/CLAUDE.md", []byte("# B 的版本\n"), 0o644)
	a.TriggerReconcile(t)
	b.TriggerReconcile(t)

	ra := th.RequireDrift(t, a.MachineID, ".claude/CLAUDE.md")
	rb := th.RequireDrift(t, b.MachineID, ".claude/CLAUDE.md")

	_, err := th.Hub.AdoptDrift([]string{ra.Id, rb.Id})
	require.ErrorIs(t, err, drift.ErrConflict)

	// 两条都还在，什么都没变
	require.Equal(t, "# A 的版本\n", string(a.ReadManaged(t, ".claude/CLAUDE.md")))
	require.Equal(t, "# B 的版本\n", string(b.ReadManaged(t, ".claude/CLAUDE.md")))
}

// spec §7.7：apply 撞上未解决的漂移 → 照常覆盖，但记录置 superseded，
// 内容追得回来。
func TestApplyOverwritesDriftAsSuperseded(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{".claude/CLAUDE.md": "# v1\n"})
	ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)

	ta.WriteManaged(t, ".claude/CLAUDE.md", []byte("# 本地改动\n"), 0o644)
	ta.TriggerReconcile(t)
	rec := th.RequireDrift(t, ta.MachineID, ".claude/CLAUDE.md")

	// 中台发布 v2，覆盖它
	th.UpdateDraft(t, setID, ".claude/CLAUDE.md", "# v2\n")
	_, err := th.Hub.PublishConfigSet(setID, "v2")
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(ta.ManagedHome, ".claude/CLAUDE.md"))
		return err == nil && string(b) == "# v2\n"
	}, 3*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		r, err := th.App.FindRecordById("drift_events", rec.Id)
		return err == nil && r.GetString("state") == "superseded"
	}, 3*time.Second, 20*time.Millisecond, "被覆盖的漂移应当置 superseded 而非删除")

	// 内容追得回来
	r, err := th.App.FindRecordById("drift_events", rec.Id)
	require.NoError(t, err)
	blobRec, err := th.App.FindRecordById("blobs", r.GetString("current_blob"))
	require.NoError(t, err)
	content, err := blobs.New(th.App).Get(blobRec.GetString("hash"))
	require.NoError(t, err)
	require.Equal(t, "# 本地改动\n", string(content))
}
```

`TestHub.UpdateDraft` 是新辅助：写一个草稿文件（`configsets.Service.SetDraftFile`）。

- [ ] **Step 2: 实现并跑通**

Run: `go test -tags=testing ./...`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add hub/ internal/testsupport/
git commit -m "feat: 收件箱 API 与端到端收编/恢复测试"
```
