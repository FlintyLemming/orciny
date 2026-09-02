package configsync_test

import (
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/configsync"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/overrides"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
	"github.com/FlintyLemming/orciny/hub/internal/variables"
	"github.com/FlintyLemming/orciny/protocol"
)

type sentMsg struct {
	machine string
	kind    protocol.Kind
	payload any
}

type fakeSender struct {
	mu     sync.Mutex
	sent   []sentMsg
	online map[string]bool
}

func (f *fakeSender) SendTo(machineID string, kind protocol.Kind, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMsg{machineID, kind, payload})
	return nil
}
func (f *fakeSender) Online(machineID string) bool { return f.online[machineID] }

func (f *fakeSender) of(kind protocol.Kind) []sentMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentMsg
	for _, m := range f.sent {
		if m.kind == kind {
			out = append(out, m)
		}
	}
	return out
}

type rig struct {
	app    *tests.TestApp
	sets   *configsets.Service
	revs   *revisions.Service
	vars   *variables.Store
	provs  *providers.Store
	sender *fakeSender
	svc    *configsync.Service
	blobs  *blobs.Store
	ovs    *overrides.Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	b := blobs.New(app)
	ev := events.NewWriter(app)
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	r := &rig{
		app:    app,
		sets:   configsets.NewService(app, b, ev),
		revs:   revisions.NewService(app, b, ev),
		vars:   variables.NewStore(app),
		provs:  providers.NewStore(app, key, ev),
		sender: &fakeSender{online: map[string]bool{}},
		blobs:  b,
	}
	r.ovs = overrides.NewService(app, b, ev, nil)
	r.svc = configsync.NewService(configsync.Deps{
		App: app, Blobs: b, Sets: r.sets, Revs: r.revs, Vars: r.vars,
		Providers: r.provs, Events: ev, Sender: r.sender, Overrides: r.ovs,
	})
	return r
}

// publish 建配置集、写文件、发布，返回配置集 id。
func (r *rig) publish(t *testing.T, files map[string][]byte) string {
	t.Helper()
	set, err := r.sets.Create("集-"+t.Name(), "")
	require.NoError(t, err)
	for path, content := range files {
		_, err := r.sets.SetDraftFile(set.Id, path, content, 0o644, nil)
		require.NoError(t, err)
	}
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	return set.Id
}

// assign 建一台机器并指派到给定配置集，返回机器 id。
func (r *rig) assign(t *testing.T, setID, fp string) string {
	t.Helper()
	m := r.machine(t, fp)
	_, err := r.sets.Assign(m, setID, configsets.ModeApply)
	require.NoError(t, err)
	return m
}

func (r *rig) machine(t *testing.T, fp string) string {
	t.Helper()
	c, err := r.app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	rec := core.NewRecord(c)
	rec.Set("fingerprint", fp)
	rec.Set("pub_key", "pk-"+fp)
	rec.Set("status", "online")
	// 真实握手会写这个字段；不写的话 M1.5 的定向版本门槛无从判定。
	rec.Set("agent_version", orciny.Version)
	require.NoError(t, r.app.Save(rec))
	r.sender.online[rec.Id] = true
	return rec.Id
}

// 只发该版本引用到的机器变量（spec §5.3）。
func TestSnapshotCarriesOnlyReferencedVariables(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp1")

	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"W":"{{var.ws}}"}`), 0o600, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	require.NoError(t, r.vars.SetVariable(m, "ws", "main"))
	require.NoError(t, r.vars.SetVariable(m, "other", "unused"))
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"ws": "main"}, snap.Variables,
		"不该把整台机器的变量都发下去")
	require.Equal(t, protocol.ModeApply, snap.Mode)
	require.NotEmpty(t, snap.Manifest)
	require.Len(t, snap.Files, 1)
	require.Equal(t, protocol.Checksum(snap.Files), snap.Checksum)
}

func TestSnapshotSurveyMode(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp2")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "survey")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, protocol.ModeSurvey, snap.Mode)
}

func TestNotifyConfigSetReachesAllAssignedMachines(t *testing.T) {
	r := newRig(t)
	m1 := r.machine(t, "fp3")
	m2 := r.machine(t, "fp4")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m1, set.Id, "apply")
	require.NoError(t, err)
	_, err = r.sets.Assign(m2, set.Id, "survey")
	require.NoError(t, err)

	require.NoError(t, r.svc.NotifyConfigSet(set.Id, rev.Id, protocol.ReasonPublished))

	sent := r.sender.of(protocol.KindConfigNotify)
	require.Len(t, sent, 2)
	for _, s := range sent {
		n := s.payload.(protocol.ConfigNotify)
		require.Equal(t, set.Id, n.ConfigSetID)
		require.Equal(t, rev.Id, n.RevisionID)
		require.Equal(t, protocol.ReasonPublished, n.Reason)
	}
}

// 暂停下发的配置集不发通知（产品 §4.2）。
func TestNotifySkipsPausedConfigSet(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp5")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	set.Set("paused", true)
	require.NoError(t, r.app.Save(set))

	require.NoError(t, r.svc.NotifyConfigSet(set.Id, rev.Id, protocol.ReasonPublished))
	require.Empty(t, r.sender.of(protocol.KindConfigNotify))
}

func TestPullSendsSnapshot(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp8")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.Pull(m, protocol.ConfigPull{})
	sent := r.sender.of(protocol.KindConfigSnapshot)
	require.Len(t, sent, 1)
}

func TestBlobRequestSendsOneMessagePerBlob(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp9")
	b := blobs.New(r.app)
	h1, err := b.Put([]byte("一"))
	require.NoError(t, err)
	h2, err := b.Put([]byte("二"))
	require.NoError(t, err)

	r.svc.BlobRequest(m, protocol.BlobRequest{Hashes: []string{h1, h2, "不存在的hash"}})

	sent := r.sender.of(protocol.KindBlobData)
	require.Len(t, sent, 3, "一条一个 blob")
	var missing int
	for _, s := range sent {
		if s.payload.(protocol.BlobData).Missing {
			missing++
		}
	}
	require.Equal(t, 1, missing, "找不到的要明确告知，让 agent 中止 apply")
}

func TestApplyAckUpdatesAssignment(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp10")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.ApplyAck(m, protocol.ApplyAck{RevisionID: rev.Id, OK: true, DurationMs: 12})

	a, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "aligned", a.GetString("state"))
	require.Equal(t, rev.Id, a.GetString("applied_revision"))
	require.Empty(t, a.GetString("last_error"))
}

func TestApplyAckFailureAndDegraded(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp11")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.ApplyAck(m, protocol.ApplyAck{
		RevisionID: rev.Id, OK: false, RolledBack: true, Error: "permission denied",
	})
	a, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "failed", a.GetString("state"))
	require.Contains(t, a.GetString("last_error"), "permission denied")

	// 回滚失败 → degraded
	r.svc.ApplyAck(m, protocol.ApplyAck{
		RevisionID: rev.Id, OK: false, RolledBack: false, Error: "回滚也失败了",
	})
	a, err = r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "degraded", a.GetString("state"))

	kinds := map[string]bool{}
	recs, err := r.app.FindAllRecords("events")
	require.NoError(t, err)
	for _, rec := range recs {
		kinds[rec.GetString("kind")] = true
	}
	require.True(t, kinds[events.KindApplyFailed])
	require.True(t, kinds[events.KindApplyRollbackFailed])
}

// 未指派的机器拉取不该报错崩掉，只是没有快照可给。
func TestPullOfUnassignedMachineIsQuiet(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp12")
	r.svc.Pull(m, protocol.ConfigPull{})
	require.Empty(t, r.sender.of(protocol.KindConfigSnapshot))
}

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

// 有覆盖层的机器拿到合并后的 hash；同配置集的其它机器拿原 hash。
func TestSnapshotAppliesOverridesPerMachine(t *testing.T) {
	r := newRig(t)
	setID := r.publish(t, map[string][]byte{
		".claude/settings.json": []byte(`{"env":{"A":"中台"}}`),
	})
	a := r.assign(t, setID, "fp-ov-a")
	b := r.assign(t, setID, "fp-ov-b")

	require.NoError(t, r.ovs.CreateJSON(a, ".claude/settings.json", "",
		[]overrides.Point{{Selector: "env.A", BaseValue: `"中台"`, MineValue: `"本机"`}}))

	snapA, err := r.svc.Snapshot(a)
	require.NoError(t, err)
	snapB, err := r.svc.Snapshot(b)
	require.NoError(t, err)

	contentA, err := r.blobs.Get(hashOf(t, snapA, ".claude/settings.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"env":{"A":"本机"}}`, string(contentA))

	contentB, err := r.blobs.Get(hashOf(t, snapB, ".claude/settings.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"env":{"A":"中台"}}`, string(contentB), "别的机器不受影响")

	require.NotEqual(t, snapA.Checksum, snapB.Checksum, "清单变了 checksum 必须跟着变")
}

// 没有覆盖层时 checksum 与 head 逐位相同——重算不是行为变更。
func TestSnapshotChecksumUnchangedWithoutOverrides(t *testing.T) {
	r := newRig(t)
	setID := r.publish(t, map[string][]byte{".claude/CLAUDE.md": []byte("规矩\n")})
	m := r.assign(t, setID, "fp-ov-c")

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	head, err := r.revs.Head(setID)
	require.NoError(t, err)
	require.Equal(t, head.GetString("checksum"), snap.Checksum)
}

// 覆盖层增删后发一条不带 RevisionID 的 ConfigNotify（spec §4.4）。
func TestNotifyOverrideSendsNotifyWithoutRevision(t *testing.T) {
	r := newRig(t)
	setID := r.publish(t, map[string][]byte{".claude/CLAUDE.md": []byte("规矩\n")})
	m := r.assign(t, setID, "fp-ov-d")

	require.NoError(t, r.svc.NotifyOverride(m))

	sent := r.sender.of(protocol.KindConfigNotify)
	require.NotEmpty(t, sent)
	n := sent[len(sent)-1].payload.(protocol.ConfigNotify)
	require.Equal(t, m, sent[len(sent)-1].machine)
	require.Equal(t, protocol.ReasonOverride, n.Reason)
	require.Empty(t, n.RevisionID, "不带 RevisionID：同版本、内容变了")
}

func hashOf(t *testing.T, snap protocol.ConfigSnapshot, path string) string {
	t.Helper()
	for _, f := range snap.Files {
		if f.Path == path {
			return f.Hash
		}
	}
	t.Fatalf("快照里没有 %s", path)
	return ""
}
