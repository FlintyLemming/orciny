package applier_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/protocol"
)

func sec() *secrets.File {
	return &secrets.File{
		Provider: map[string]string{"claude.auth_token": "sk-real-value"},
		Vars:     map[string]string{"ws": "main"},
		Machine:  map[string]string{"hostname": "mac", "os": "darwin", "arch": "arm64", "name": "主力"},
	}
}

// snapFor 造一份只含这些文件的快照，内容取自 content。
func snapFor(content map[string][]byte, modes map[string]uint32) (protocol.ConfigSnapshot, map[string][]byte) {
	snap := protocol.ConfigSnapshot{ConfigSetID: "set", RevisionID: "rev", Seq: 1}
	byHash := map[string][]byte{}
	for rel, c := range content {
		h := blobcache.Hash(c)
		mode := modes[rel]
		if mode == 0 {
			mode = 0o644
		}
		snap.Files = append(snap.Files, protocol.FileEntry{
			Path: rel, Hash: h, Size: uint32(len(c)), Mode: mode,
		})
		byHash[h] = c
	}
	snap.Checksum = protocol.Checksum(snap.Files)
	return snap, byHash
}

func stepFor(t *testing.T, p applier.Plan, rel string) applier.Step {
	t.Helper()
	for _, s := range p.Steps {
		if s.Rel == rel {
			return s
		}
	}
	t.Fatalf("plan 里没有 %s", rel)
	return applier.Step{}
}

func TestPlanCreateWhenAbsent(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("# 规矩")}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Len(t, p.Steps, 1)
	require.Equal(t, protocol.ActionCreate, p.Steps[0].Action)
	require.Equal(t, []byte("# 规矩"), p.Steps[0].Content)
}

func TestPlanSkipWhenRenderedHashMatches(t *testing.T) {
	content := []byte(`{"K":"{{provider.claude.auth_token}}"}`)
	snap, blobs := snapFor(map[string][]byte{".claude/settings.json": content},
		map[string]uint32{".claude/settings.json": 0o600})

	rendered := []byte(`{"K":"sk-real-value"}`)
	st := &state.State{Files: map[string]state.FileState{
		".claude/settings.json": {
			Blob:     blobcache.Hash(content),
			Rendered: blobcache.Hash(rendered),
			Mode:     0o600,
		},
	}}

	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, protocol.ActionSkip, stepFor(t, p, ".claude/settings.json").Action)
	require.Equal(t, 0, p.Writes(), "全 skip 时零写入")
}

// 凭据轮换：blob 没变，但渲染后的内容变了 → 必须 overwrite（spec §5.1）。
func TestPlanOverwriteAfterRotation(t *testing.T) {
	content := []byte(`{"K":"{{provider.claude.auth_token}}"}`)
	snap, blobs := snapFor(map[string][]byte{".claude/settings.json": content},
		map[string]uint32{".claude/settings.json": 0o600})

	old := []byte(`{"K":"sk-OLD-value"}`)
	st := &state.State{Files: map[string]state.FileState{
		".claude/settings.json": {
			Blob:     blobcache.Hash(content),
			Rendered: blobcache.Hash(old),
			Mode:     0o600,
		},
	}}

	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, protocol.ActionOverwrite, stepFor(t, p, ".claude/settings.json").Action)
	require.Equal(t, []byte(`{"K":"sk-real-value"}`), stepFor(t, p, ".claude/settings.json").Content)
}

// 上一版有、本版无 → delete。
func TestPlanDeleteWhenDroppedFromRevision(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("keep")}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/CLAUDE.md": {Blob: "x", Rendered: "y", Mode: 0o644},
		".claude/gone.md":   {Blob: "a", Rendered: "b", Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, protocol.ActionDelete, stepFor(t, p, ".claude/gone.md").Action)
	require.Nil(t, stepFor(t, p, ".claude/gone.md").Content)
}

func TestPlanMergeForKeysMode(t *testing.T) {
	content := []byte(`{"mcpServers":{"a":{}}}`)
	h := blobcache.Hash(content)
	snap := protocol.ConfigSnapshot{
		Files: []protocol.FileEntry{{
			Path: ".claude.json", Hash: h, Size: uint32(len(content)),
			Mode: 0o644, Keys: []string{"mcpServers"},
		}},
	}
	p, err := applier.BuildPlan(snap, nil, map[string][]byte{h: content}, sec().Lookup)
	require.NoError(t, err)
	s := stepFor(t, p, ".claude.json")
	require.Equal(t, protocol.ActionMerge, s.Action, "keys 模式永远是 merge")
	require.Equal(t, []string{"mcpServers"}, s.Keys)
}

// 忽略清单里的路径不进 plan（spec §8.4）。
func TestPlanRespectsIgnorePaths(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":             []byte("a"),
		".claude/skills/scratch/tmp.md": []byte("b"),
	}, nil)
	snap.IgnorePaths = []string{".claude/skills/scratch/**"}

	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Len(t, p.Steps, 1)
	require.Equal(t, ".claude/CLAUDE.md", p.Steps[0].Rel)
}

// 未定义引用：拒绝该文件并记进 Skip，其余文件照常（spec §6.1）。
func TestPlanRefusesFileWithUndefinedRef(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":     []byte("ok"),
		".claude/settings.json": []byte(`{"K":"{{var.deleted}}"}`),
	}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Len(t, p.Steps, 1)
	require.Equal(t, ".claude/CLAUDE.md", p.Steps[0].Rel)
	require.Len(t, p.Skip, 1)
	require.Equal(t, ".claude/settings.json", p.Skip[0].Rel)
	require.Contains(t, p.Skip[0].Reason, "var.deleted")
}

// 缺内容就整体不动：宁可不 apply，也不能只写一半（spec §7.4）。
func TestPlanFailsWhenContentMissing(t *testing.T) {
	snap, _ := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("x")}, nil)
	_, err := applier.BuildPlan(snap, nil, map[string][]byte{}, sec().Lookup)
	require.Error(t, err)
}

// 路径安全在这里也要判一次（spec §3.3：展开与落盘两处都判）。
func TestPlanRejectsUnsafePath(t *testing.T) {
	content := []byte("x")
	h := blobcache.Hash(content)
	snap := protocol.ConfigSnapshot{Files: []protocol.FileEntry{
		{Path: "../escape", Hash: h, Size: 1, Mode: 0o644},
	}}
	_, err := applier.BuildPlan(snap, nil, map[string][]byte{h: content}, sec().Lookup)
	require.Error(t, err)
}

func TestPlanIsSortedByPath(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{
		".claude/z.md": []byte("z"),
		".claude/a.md": []byte("a"),
		".claude.json": []byte("{}"),
	}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, []string{".claude.json", ".claude/a.md", ".claude/z.md"},
		[]string{p.Steps[0].Rel, p.Steps[1].Rel, p.Steps[2].Rel})
}
