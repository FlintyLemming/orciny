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

	// resolved_revision 是 relation，必须指向真实 revision 记录。
	require.NoError(t, r.svc.Supersede(r.machineID, []string{".claude/CLAUDE.md"}, r.revID))

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "superseded", rec.GetString("state"))
	require.Equal(t, blobID, rec.GetString("current_blob"),
		"内容要留着——用户还能在「已被覆盖」筛选里把它捞回来")
	require.Equal(t, r.revID, rec.GetString("resolved_revision"))
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

	require.NoError(t, r.svc.Supersede(r.machineID, []string{"a"}, r.revID))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "ignored", rec.GetString("state"), "已忽略的不该被改成 superseded")
}
