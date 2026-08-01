package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestHandleReportCreatesOpenEvents(t *testing.T) {
	r := newRig(t)
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
