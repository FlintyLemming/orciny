package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/protocol"
)

// jsonDrift 让本机在 settings.json 上产生一条 open 漂移。
func (r *rig) jsonDrift(t *testing.T, base, cur []byte) string {
	t.Helper()
	set, err := r.sets.Create("覆盖层用", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/settings.json", base, 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, "apply")
	require.NoError(t, err)
	r.setID, r.revID = set.Id, rev.Id

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified,
		Mode: 0o644, Content: cur,
	})
	return r.driftsByPath(t)[".claude/settings.json"].Id
}

func TestOverrideCreatesPointsAndMarksDrift(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"A":"中台","B":"共用"}}`),
		[]byte(`{"env":{"A":"本机","B":"共用"}}`))

	require.NoError(t, r.svc.Override([]string{id}, nil, nil))

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1)
	require.Equal(t, "json_key", ovs[0].GetString("kind"))
	require.Equal(t, "env.A", ovs[0].GetString("selector"))
	require.JSONEq(t, `"本机"`, ovs[0].GetString("mine_value"))
	require.Equal(t, id, ovs[0].GetString("origin_drift"))

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "overridden", rec.GetString("state"))
	r.requireEvent(t, "override.created")
}

func TestOverrideRespectsSelectorSelection(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"A":"中台","B":"中台"}}`),
		[]byte(`{"env":{"A":"本机","B":"本机"}}`))

	require.NoError(t, r.svc.Override([]string{id},
		map[string]drift.Selection{id: {Given: true, Selectors: []string{"env.A"}}}, nil))

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1, "没勾的差异点不建记录")
	require.Equal(t, "env.A", ovs[0].GetString("selector"))
}

func TestOverrideEmptySelectionIsError(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	err := r.svc.Override([]string{id},
		map[string]drift.Selection{id: {Given: true}}, nil)
	require.ErrorIs(t, err, drift.ErrNoPoints)
	require.Empty(t, r.overridesOf(t, r.machineID), "报错就什么都不动")
}

func TestOverrideRejectsBindingDrift(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`),
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`))
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrBindingDrift)
	require.Empty(t, r.overridesOf(t, r.machineID))
}

func TestOverrideNeedsReviewForRestorePartial(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	rec.Set("restore_partial", true)
	require.NoError(t, r.app.Save(rec))

	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNeedsReview)
	require.NoError(t, r.svc.Override([]string{id}, nil, []string{id}))
}

func TestOverrideRejectsTruncated(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	rec.Set("truncated", true)
	require.NoError(t, r.app.Save(rec))
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNotOverridable)
}

func TestOverrideRejectsDeletedKind(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	rec.Set("kind", "deleted")
	require.NoError(t, r.app.Save(rec))
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNotOverridable)
}

// keys 模式：受管键之外建覆盖层毫无作用，会得到「设置好了但什么都没发生」。
func TestOverrideRejectsSelectorOutsideManagedKeys(t *testing.T) {
	r := newRig(t)
	set, err := r.sets.Create("keys 模式", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude.json",
		[]byte(`{"mcpServers":{"a":1},"projects":{"x":1}}`), 0o644, []string{"mcpServers"})
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, "apply")
	require.NoError(t, err)
	r.report(t, protocol.DriftItem{
		Path: ".claude.json", Kind: protocol.DriftModified, Mode: 0o644,
		Content: []byte(`{"mcpServers":{"a":1},"projects":{"x":2}}`),
	})
	id := r.driftsByPath(t)[".claude.json"].Id

	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNotOverridable)

	// 受管键内的差异点则允许。
	r.resolve(t, ".claude.json", "superseded")
	r.report(t, protocol.DriftItem{
		Path: ".claude.json", Kind: protocol.DriftModified, Mode: 0o644,
		Content: []byte(`{"mcpServers":{"a":9},"projects":{"x":1}}`),
	})
	id2 := r.driftsByPath(t)[".claude.json"].Id
	require.NoError(t, r.svc.Override([]string{id2}, nil, nil))
}

// 已退管的路径不能建覆盖层：中台根本不下发它，覆盖层无处可盖。
func TestOverrideRejectsIgnoredPath(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	r.addIgnoreRule(t, r.machineID, ".claude/settings.json")
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrPathUnmanaged)

	// 全局规则同样拦住。
	r2 := newRig(t)
	id2 := r2.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	r2.addIgnoreRule(t, "", ".claude/settings.json")
	require.ErrorIs(t, r2.svc.Override([]string{id2}, nil, nil), drift.ErrPathUnmanaged)
}

// 新的替换旧的：前缀关系的既有记录被删掉，并记一条 override.replaced。
//
// 要造出 env 这个**祖先** selector，中台侧的 env 必须不是对象——Split
// 在两侧都是对象时一定往下降，只会给出 env.A 这样的叶子。这里让中台把
// env 改成标量，本机仍是对象，于是差异点就落在 env 这一层。
func TestOverrideReplacesOverlappingSelector(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"A":"中台"}}`), []byte(`{"env":{"A":"本机"}}`))
	require.NoError(t, r.svc.Override([]string{id}, nil, nil)) // env.A

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1)
	require.Equal(t, "env.A", ovs[0].GetString("selector"))

	// 中台把 env 从对象改成标量并发布。
	_, err := r.sets.SetDraftFile(r.setID, ".claude/settings.json",
		[]byte(`{"env":"关掉"}`), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(r.setID, "v2", "publish")
	require.NoError(t, err)

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified, Mode: 0o644,
		Content: []byte(`{"env":{"A":"本机"}}`),
	})
	id2 := r.driftsByPath(t)[".claude/settings.json"].Id
	require.NoError(t, r.svc.Override([]string{id2},
		map[string]drift.Selection{id2: {Given: true, Selectors: []string{"env"}}}, nil))

	ovs = r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1, "env 覆盖了 env.A")
	require.Equal(t, "env", ovs[0].GetString("selector"))
	r.requireEvent(t, "override.replaced")
}

// 文本文件：一条记录承载整个文件，勾选塌缩进 mine_blob。
func TestOverrideTextKeepsSelectedHunks(t *testing.T) {
	r := newRig(t)
	base := []byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nl16\n")
	cur := []byte("L1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nL16\n")

	set, err := r.sets.Create("文本", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/CLAUDE.md", base, 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, "apply")
	require.NoError(t, err)
	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Mode: 0o644, Content: cur,
	})
	id := r.driftsByPath(t)[".claude/CLAUDE.md"].Id

	// 只留第一处 hunk。
	require.NoError(t, r.svc.Override([]string{id},
		map[string]drift.Selection{id: {Given: true, Hunks: []int{0}}}, nil))

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1)
	require.Equal(t, "text", ovs[0].GetString("kind"))
	require.Empty(t, ovs[0].GetString("selector"))

	blobRec, err := r.app.FindRecordById("blobs", ovs[0].GetString("mine_blob"))
	require.NoError(t, err)
	mine, err := r.blobs.Get(blobRec.GetString("hash"))
	require.NoError(t, err)
	require.Contains(t, string(mine), "L1\n")
	require.Contains(t, string(mine), "l16\n", "没勾的 hunk 退回基线")
}

// 建覆盖层后要发一条 ConfigNotify，不必等下一次拉取。
func TestOverrideNotifiesMachine(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	require.NoError(t, r.svc.Override([]string{id}, nil, nil))

	sent := r.sender.of(protocol.KindConfigNotify)
	require.NotEmpty(t, sent)
	n := sent[len(sent)-1].payload.(protocol.ConfigNotify)
	require.Equal(t, protocol.ReasonOverride, n.Reason)
}
