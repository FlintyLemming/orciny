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

func readManaged(t *testing.T, r *rig, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.home, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(b)
}
