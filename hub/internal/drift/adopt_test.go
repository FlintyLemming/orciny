package drift_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
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

// seedProviderFor 建一条只配了 claude 端点的 Provider，返回记录 id。
func seedProviderFor(t *testing.T, r *rig, name string) string {
	t.Helper()
	c, err := r.app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", name)
	p.Set("key_cipher", "x")
	p.Set("key_last4", "1234")
	p.Set("claude", providers.ClaudeEndpoint{
		Endpoint: providers.Endpoint{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: providers.AuthToken,
			KeyLast4:  "1234",
			Models:    []string{"glm-5.2"},
		},
	})
	require.NoError(t, r.app.Save(p))
	return p.Id
}

// 收编改的是文件，不是绑定：新 Revision 沿用 head 的绑定。
// 不沿用的话，收编一次就等于顺手解绑，且没有任何提示。
func TestAdoptKeepsHeadBinding(t *testing.T) {
	r := newRig(t)
	provID := seedProviderFor(t, r, "智谱 GLM · 个人")

	set, err := r.sets.Create("主力", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("原始规矩\n"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, r.sets.SetDraftBinding(set.Id, &providers.Binding{
		Provider: provID,
		Models: providers.ModelSlots{
			Main: "glm-5.1", Opus: "glm-5.1", Sonnet: "glm-5.1", Haiku: "glm-5.1",
		},
	}))
	rev1, err := r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, configsets.ModeApply)
	require.NoError(t, err)
	r.setID = set.Id
	r.revID = rev1.Id

	wantBinding, err := r.revs.BindingOf(rev1.Id)
	require.NoError(t, err)
	require.NotNil(t, wantBinding)

	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified,
		Content: []byte("# 改过的规矩\n"), Mode: 0o644,
	})
	rev, err := r.svc.Adopt([]string{r.openDrifts(t)[0].Id})
	require.NoError(t, err)

	got, err := r.revs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, wantBinding, got, "收编不得顺手解绑")

	reloaded, err := r.app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.Equal(t, provID, reloaded.GetString("head_provider"))
}
