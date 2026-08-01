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
