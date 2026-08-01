# 子计划 15 · survey 模式与状态丢失自愈

**前置**：08、14
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`survey` 模式的全量对账上报、`state.json` 丢失时的自愈路径、`degraded` 的人工解除。

**为什么 survey 复用漂移路径（spec §7.6 / 取舍记录 #6）**：一条机制同时解决三个问题——

1. 第二台机器接入时不会被无声覆盖（产品 §4.2 要求的「保持本机现状」）
2. 「用户在本机的改动比中台的基线更好」是个人版的常态，第一时间就能被收编，而不是先被覆盖再让人去恢复
3. **`state.json` 丢失时的自愈**：agent 上报状态丢失，hub 把 assignment 打回 `survey`，全量对账进收件箱。绝不因为「我不知道基线是什么」就直接覆盖用户的文件

代价只有 `ConfigSnapshot` 多一个 `Mode` 字段。

---

### Task 1: survey 模式的全量对账

**Files:**
- Modify: `agent/internal/syncer/syncer.go`（`applyPending` 的 survey 分支）
- Test: `agent/internal/syncer/survey_test.go`

**Interfaces:**
- Produces: survey 快照到达时 → 用快照清单当基线做一次全量对账 → `DriftReport{Full: true}`，**不写任何文件**

**关键**：survey 的基线不是 `state.json`（本机可能根本没有），而是**快照里的清单**。因此要用快照造一份「虚拟 state」喂给 watcher 的 `Scan`。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/syncer/survey_test.go`：

```go
package syncer_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/protocol"
)

// survey：不写盘，把全部差异作为漂移上报（spec §7.6）。
func TestSurveyReportsDifferencesWithoutWriting(t *testing.T) {
	r := newRig(t)
	// 本机已有内容，且与中台基线不同
	writeManaged(t, r, ".claude/CLAUDE.md", "# 本机自己的规矩\n")
	writeManaged(t, r, ".claude/skills/我的/SKILL.md", "本机独有\n")

	snap, blobs := snapshotOf(t, map[string]string{
		".claude/CLAUDE.md":     "# 中台的规矩\n",
		".claude/settings.json": `{"model":"opus"}`,
	})
	snap.Mode = protocol.ModeSurvey

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}

	// 一个字节都不能写
	require.Equal(t, "# 本机自己的规矩\n", readManaged(t, r, ".claude/CLAUDE.md"))
	require.NoFileExists(t, filepath.Join(r.home, ".claude/settings.json"))
	require.Empty(t, r.out.of(protocol.KindApplyAck), "survey 不产生 apply 回执")

	// 全部差异进漂移上报
	var items []protocol.DriftItem
	for _, rep := range driftReports(t, r) {
		require.True(t, rep.Full, "survey 的对账是全量的")
		items = append(items, rep.Items...)
	}
	byPath := map[string]protocol.DriftItem{}
	for _, it := range items {
		byPath[it.Path] = it
	}

	require.Contains(t, byPath, ".claude/CLAUDE.md")
	require.Equal(t, protocol.DriftModified, byPath[".claude/CLAUDE.md"].Kind)
	require.Equal(t, "# 本机自己的规矩\n", string(byPath[".claude/CLAUDE.md"].Content))

	require.Contains(t, byPath, ".claude/skills/我的/SKILL.md")
	require.Equal(t, protocol.DriftAdded, byPath[".claude/skills/我的/SKILL.md"].Kind)

	// 中台有、本机没有 → deleted（从本机视角看是「基线里有但磁盘上没有」）
	require.Contains(t, byPath, ".claude/settings.json")
	require.Equal(t, protocol.DriftDeleted, byPath[".claude/settings.json"].Kind)
}

// survey 也要写 state.json：记下 mode 与基线，否则重启后又不知道自己在 survey。
func TestSurveyPersistsStateWithoutFiles(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/CLAUDE.md", "本机内容\n")
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "中台内容\n"})
	snap.Mode = protocol.ModeSurvey

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}

	st, err := state.Load(r.dir)
	require.NoError(t, err)
	require.Equal(t, "survey", st.Mode)
	require.Equal(t, snap.RevisionID, st.Revision)
	require.NotEmpty(t, st.Files, "基线清单要记下来，供对账用")
	// 但 rendered 必须为空——磁盘上根本没写过，填一个假 hash 会让下次
	// 对账以为「一致」
	for _, fs := range st.Files {
		require.Empty(t, fs.Rendered)
	}
}

// 本机内容与中台一致时，survey 不产生任何漂移。
func TestSurveyOfAlignedMachineIsQuiet(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/CLAUDE.md", "一样的内容\n")
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "一样的内容\n"})
	snap.Mode = protocol.ModeSurvey

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	require.Empty(t, driftReports(t, r))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/syncer/ -run Survey -v`
Expected: FAIL，survey 分支目前只打日志

- [ ] **Step 3: 实现**

改 `applyPending` 的 survey 分支：

```go
	// survey 模式不写盘（spec §7.6）：只做一次全量对账，把所有差异作为
	// 漂移上报，由用户在收件箱里逐条决定收编还是恢复。
	if snap.Mode == protocol.ModeSurvey {
		s.survey(snap, content)
		return
	}
```

新增 `survey`：

```go
// survey 用快照清单当基线做一次全量对账。
//
// 基线不能取 state.json ——本机可能根本没有（第一次指派、或者状态丢失）。
// 因此用快照造一份「虚拟 state」：Blob 填清单里的 hash，Rendered 留空。
// Rendered 留空是要紧的：填一个假值会让对账误判「一致」而漏报。
func (s *Syncer) survey(snap protocol.ConfigSnapshot, content map[string][]byte) {
	sec, err := secrets.Load(s.d.Dir)
	if err != nil {
		s.log.Error("读取凭据缓存失败", "error", err)
		return
	}

	virtual := &state.State{
		ConfigSet: snap.ConfigSetID,
		Revision:  snap.RevisionID,
		Seq:       snap.Seq,
		Checksum:  snap.Checksum,
		Mode:      "survey",
		Health:    state.HealthOK,
		Files:     map[string]state.FileState{},
		Ignored:   snap.IgnorePaths,
		Manifest:  snap.Manifest,
		AppliedAt: s.d.Clock.Now().UTC(),
	}
	for _, f := range snap.Files {
		virtual.Files[f.Path] = state.FileState{
			Blob: f.Hash, Mode: f.Mode, Size: f.Size, Keys: f.Keys,
			// Rendered 故意留空：磁盘上从未写过这份内容。
		}
	}
	// 内容也进本地缓存：收编 / 恢复 / 转 apply 时都要用得到，
	// 而且它们可能发生在 hub 离线的时候。
	cache := blobcache.New(s.d.Dir)
	for hash, raw := range content {
		if err := cache.PutHash(hash, raw); err != nil {
			s.log.Warn("缓存基线内容失败", "hash", hash, "error", err)
		}
	}

	if err := state.Save(s.d.Dir, virtual); err != nil {
		s.log.Error("写 state.json 失败", "error", err)
	}
	s.mu.Lock()
	s.st = virtual
	s.sec = sec
	w := s.watcher
	s.mu.Unlock()

	if w == nil {
		s.log.Warn("watcher 未启动，survey 无法对账")
		return
	}
	m, err := s.manifestOf(virtual)
	if err != nil {
		s.log.Error("解析 manifest 失败", "error", err)
		return
	}
	if err := w.Reload(virtual, m, sec); err != nil {
		s.log.Error("重载对账基线失败", "error", err)
		return
	}
	items, err := w.Scan(true)
	if err != nil {
		s.log.Error("全量对账失败", "error", err)
		return
	}
	s.log.Info("survey 全量对账完成", "revision", snap.RevisionID, "drifts", len(items))
	if err := s.Report(items, true); err != nil {
		s.log.Warn("上报对账结果失败", "error", err)
	}
}
```

> watcher 的 `Scan` 对「Rendered 为空」的基线条目要判成 `modified`（磁盘上有）或 `deleted`（磁盘上没有）。子计划 12 的实现里，`Rendered` 为空与磁盘 hash 必然不等，因此磁盘上有的自动落进 `modified`；磁盘上没有的走 `deleted` 分支。无需额外改动，但要在 `scan.go` 里加一条注释说明这个依赖。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/syncer/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/internal/syncer/
git commit -m "feat: survey 模式的全量对账"
```

---

### Task 2: 状态丢失自愈

**Files:**
- Modify: `agent/internal/syncer/syncer.go`（`pull` 携带状态标记）
- Modify: `hub/internal/configsync/service.go`（`Pull` 处理状态丢失）
- Test: `agent/internal/syncer/syncer_test.go`、`hub/internal/configsync/service_test.go`

**Interfaces:**
- Produces: `ConfigPull.Have` 为空且本机 `state.json` 缺失时，hub 把 assignment 打回 `survey`

**规则（spec §7.6 第 3 条）**：绝不因为「我不知道基线是什么」就直接覆盖用户的文件。

**判定办法**：`ConfigPull.Have` 是 agent 已应用的 RevisionID。它为空有两种情形——从未 apply 过（新机器）、或 `state.json` 丢了。两者的正确处置**相同**：都该走 survey。因此规则简化为「`Have` 为空 且 assignment 已经有 `applied_revision` → 状态丢失，打回 survey」；`applied_revision` 也为空说明是新机器，按 UI 上指派时选的模式走。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsync/service_test.go`：

```go
// 状态丢失自愈：曾经对齐过的机器突然说「我什么都没有」，
// 只可能是 state.json 丢了。绝不覆盖用户的文件，一律打回 survey
// （spec §7.6 第 3 条）。
func TestPullWithLostStateFallsBackToSurvey(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-lost")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	assign, err := r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)
	assign.Set("applied_revision", rev.Id)
	assign.Set("state", "aligned")
	require.NoError(t, r.app.Save(assign))

	// agent 报告「我没有任何已应用的版本」
	r.svc.Pull(m, protocol.ConfigPull{Have: ""})

	sent := r.sender.of(protocol.KindConfigSnapshot)
	require.Len(t, sent, 1)
	require.Equal(t, protocol.ModeSurvey, sent[0].payload.(protocol.ConfigSnapshot).Mode,
		"状态丢失必须打回 survey，绝不直接覆盖")

	got, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "survey", got.GetString("mode"), "指派模式也要落库改掉")
	require.Equal(t, "pending", got.GetString("state"))
}

// 新机器（从未 apply 过）不算状态丢失，按指派时选的模式走。
func TestPullOfFreshMachineKeepsApplyMode(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-fresh")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.Pull(m, protocol.ConfigPull{Have: ""})
	sent := r.sender.of(protocol.KindConfigSnapshot)
	require.Len(t, sent, 1)
	require.Equal(t, protocol.ModeApply, sent[0].payload.(protocol.ConfigSnapshot).Mode)
}

// 版本号对不上（比如 agent 停机期间中台发了新版）不算状态丢失。
func TestPullWithStaleRevisionKeepsApplyMode(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-stale")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("v1"), 0o644, nil)
	require.NoError(t, err)
	v1, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("v2"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	assign, err := r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)
	assign.Set("applied_revision", v1.Id)
	assign.Set("state", "aligned")
	require.NoError(t, r.app.Save(assign))

	r.svc.Pull(m, protocol.ConfigPull{Have: v1.Id})
	sent := r.sender.of(protocol.KindConfigSnapshot)
	require.Equal(t, protocol.ModeApply, sent[0].payload.(protocol.ConfigSnapshot).Mode)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/configsync/ -run LostState -v`
Expected: FAIL，快照仍是 apply 模式

- [ ] **Step 3: 实现**

`configsync.Service.Pull` 在组装快照之前插入判定：

```go
func (s *Service) Pull(machineID string, p protocol.ConfigPull) {
	// 状态丢失自愈（spec §7.6 第 3 条）：曾经对齐过的机器突然说
	// 「我没有任何已应用的版本」，只可能是 state.json 丢了或损坏了。
	// 此刻 agent 不知道基线是什么，直接下发 apply 会用中台内容盖掉
	// 用户机器上的一切。一律打回 survey，让差异先进收件箱。
	if p.Have == "" {
		if assign, err := s.d.Sets.Assignment(machineID); err == nil &&
			assign.GetString("applied_revision") != "" &&
			assign.GetString("mode") != configsets.ModeSurvey {

			s.log.Warn("机器报告状态丢失，打回 survey 模式", "machine", machineID,
				"applied_revision", assign.GetString("applied_revision"))
			assign.Set("mode", configsets.ModeSurvey)
			assign.Set("state", configsets.StatePending)
			assign.Set("applied_revision", "")
			assign.Set("last_error", "本机状态丢失，已转为「先看看」模式，差异见收件箱")
			if err := s.d.App.Save(assign); err != nil {
				s.log.Warn("保存指派状态失败", "machine", machineID, "error", err)
			}
			if err := s.d.Events.Write(events.KindAssignChanged, machineID, map[string]any{
				"reason": "state_lost", "mode": configsets.ModeSurvey,
			}); err != nil {
				s.log.Warn("写 assign.changed 事件失败", "error", err)
			}
		}
	}

	snap, err := s.Snapshot(machineID)
	// ...原有逻辑不变
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/configsync/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsync/
git commit -m "feat: state.json 丢失时自动打回 survey"
```

---

### Task 3: degraded 的人工解除

**Files:**
- Modify: `hub/api.go`
- Modify: `hub/internal/routes/config.go`
- Test: `hub/internal/configsync/service_test.go`

**Interfaces:**
- Produces:
  ```go
  func (h *Hub) ClearDegraded(machineID string) error
  // POST /api/orciny/machines/{id}/clear-degraded
  ```

`degraded` 是回滚都失败之后的终态（spec §7.4 第 2 条）：agent 不再自动 apply 任何后续版本，直到人工在 UI 上确认解除。解除要做两件事：hub 侧把 assignment 打回 `survey`（不知道机器被写成什么样了，不能直接 apply），agent 侧把 `state.Health` 改回 `ok`。

- [ ] **Step 1: 写失败的测试**

```go
// degraded 解除：hub 侧打回 survey，并通知 agent 清掉 health。
func TestClearDegradedResetsToSurvey(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-degraded")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.ApplyAck(m, protocol.ApplyAck{RevisionID: rev.Id, OK: false, RolledBack: false,
		Error: "回滚也失败了"})
	a, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "degraded", a.GetString("state"))

	require.NoError(t, r.svc.ClearDegraded(m))

	a, err = r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "survey", a.GetString("mode"),
		"不知道机器被写成什么样了，不能直接 apply")
	require.Equal(t, "pending", a.GetString("state"))
	require.Empty(t, a.GetString("last_error"))

	// 通知 agent 重新拉取（它会拿到一份 survey 快照）
	require.NotEmpty(t, r.sender.of(protocol.KindConfigNotify))
}

func TestClearDegradedOnHealthyMachineIsNoop(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-ok")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	require.NoError(t, r.svc.ClearDegraded(m))
	a, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "apply", a.GetString("mode"), "健康的机器不该被改成 survey")
}
```

- [ ] **Step 2: 实现**

`configsync.Service` 加：

```go
// ClearDegraded 解除机器的 degraded 状态（spec §7.4 第 2 条）。
//
// 打回 survey 而不是 apply：一次失败的 apply 加上一次失败的回滚之后，
// 谁也不知道机器上现在是什么样子。让差异先进收件箱，由人看过再决定。
func (s *Service) ClearDegraded(machineID string) error {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		return err
	}
	if assign.GetString("state") != configsets.StateDegraded {
		return nil
	}
	assign.Set("state", configsets.StatePending)
	assign.Set("mode", configsets.ModeSurvey)
	assign.Set("last_error", "")
	if err := s.d.App.Save(assign); err != nil {
		return fmt.Errorf("configsync: 保存指派状态: %w", err)
	}
	if err := s.d.Events.Write(events.KindAssignChanged, machineID, map[string]any{
		"reason": "degraded_cleared", "mode": configsets.ModeSurvey,
	}); err != nil {
		s.log.Warn("写 assign.changed 事件失败", "error", err)
	}
	return s.NotifyMachine(machineID, protocol.ReasonAssigned)
}
```

agent 侧：收到 survey 快照时 `survey()` 已经把 `Health` 写成 `ok`（虚拟 state 里 `Health: state.HealthOK`），因此不需要额外的协议消息。在 `survey()` 的注释里点明这一点。

`hub/api.go` 加 `func (h *Hub) ClearDegraded(machineID string) error { return h.sync.ClearDegraded(machineID) }`，路由加 `POST /api/orciny/machines/{id}/clear-degraded`。

- [ ] **Step 3: 跑测试确认通过**

Run: `go test -tags=testing ./hub/...`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add hub/
git commit -m "feat: degraded 状态的人工解除"
```

---

### Task 4: 端到端验证（DoD 3、12）

**Files:**
- Create: `internal/testsupport/survey_test.go`

- [ ] **Step 1: 写测试**

```go
//go:build testing

// DoD 第 3 条：第三台机器以 survey 模式指派 → 不写盘，
// 本机与基线的全部差异出现在收件箱。
func TestSurveyModeKeepsMachineIntact(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())

	// 这台机器已经有自己的一套东西
	ta.WriteManaged(t, ".claude/CLAUDE.md", []byte("# 我自己的规矩\n"), 0o644)
	ta.WriteManaged(t, ".claude/skills/我的/SKILL.md", []byte("本机独有\n"), 0o644)

	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md":     "# 中台的规矩\n",
		".claude/settings.json": `{"model":"opus"}`,
	})
	ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeSurvey)

	// 一个字节都没被改
	require.Equal(t, "# 我自己的规矩\n", string(ta.ReadManaged(t, ".claude/CLAUDE.md")))
	require.NoFileExists(t, filepath.Join(ta.ManagedHome, ".claude/settings.json"))

	// 差异全部进了收件箱
	md := th.RequireDrift(t, ta.MachineID, ".claude/CLAUDE.md")
	require.Equal(t, "modified", md.GetString("kind"))
	require.Contains(t, md.GetString("diff"), "我自己的规矩")

	mine := th.RequireDrift(t, ta.MachineID, ".claude/skills/我的/SKILL.md")
	require.Equal(t, "added", mine.GetString("kind"))

	th.RequireDrift(t, ta.MachineID, ".claude/settings.json")

	// 收编本机的 CLAUDE.md，中台随之更新
	revID, err := th.Hub.AdoptDrift([]string{md.Id, mine.Id})
	require.NoError(t, err)
	files, err := revisions.NewService(th.App, blobs.New(th.App), events.NewWriter(th.App)).Files(revID)
	require.NoError(t, err)
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	require.Contains(t, paths, ".claude/skills/我的/SKILL.md")
}

// DoD 第 12 条：删除 state.json 后重启 agent → 进入 survey 全量对账，
// 不覆盖任何用户文件。
func TestStateLossFallsBackToSurvey(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md": "# 中台版本\n",
	})
	sess := ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)
	require.Equal(t, "# 中台版本\n", string(ta.ReadManaged(t, ".claude/CLAUDE.md")))

	// 用户在本机改了内容，然后 state.json 丢了（磁盘故障、误删、迁移）
	ta.WriteManaged(t, ".claude/CLAUDE.md", []byte("# 用户改的内容\n"), 0o644)
	require.NoError(t, sess.Close())
	require.NoError(t, os.Remove(state.Path(ta.Dir)))

	// agent 重启
	ta.RunSync(t, th)

	// 绝不覆盖：用户的内容还在
	require.Eventually(t, func() bool {
		a, err := th.App.FindRecordsByFilter("assignments", "machine = {:m}", "", 1, 0,
			map[string]any{"m": ta.MachineID})
		return err == nil && len(a) == 1 && a[0].GetString("mode") == "survey"
	}, 3*time.Second, 20*time.Millisecond, "状态丢失应当打回 survey")

	require.Equal(t, "# 用户改的内容\n", string(ta.ReadManaged(t, ".claude/CLAUDE.md")),
		"不知道基线是什么时绝不覆盖用户的文件")

	// 差异进了收件箱
	th.RequireDrift(t, ta.MachineID, ".claude/CLAUDE.md")
}
```

- [ ] **Step 2: 跑通并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add internal/testsupport/
git commit -m "test: survey 模式与状态丢失自愈的端到端验证"
```
