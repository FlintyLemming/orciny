package applier_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/protocol"
)

func keysSnap(t *testing.T, content string) (protocol.ConfigSnapshot, map[string][]byte) {
	t.Helper()
	h := blobcache.Hash([]byte(content))
	return protocol.ConfigSnapshot{
		ConfigSetID: "set", RevisionID: "rev", Seq: 1,
		Files: []protocol.FileEntry{{
			Path: ".claude.json", Hash: h, Size: uint32(len(content)),
			Mode: 0o644, Keys: []string{"mcpServers"},
		}},
	}, map[string][]byte{h: []byte(content)}
}

// DoD 第 7 条：非受管键仍在，且键顺序未被重排。
func TestMergeKeepsUnmanagedKeysAndOrder(t *testing.T) {
	fx := newFixture(t)
	original := `{
  "numStartups": 42,
  "mcpServers": {
    "old": {"command": "old"}
  },
  "projects": {
    "/Users/x": {"lastCost": 1.5}
  }
}`
	fx.write(t, ".claude.json", original, 0o644)

	snap, blobs := keysSnap(t, `{"mcpServers":{"new":{"command":"new"}}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK, "错误：%s", ack.Error)

	got := fx.read(t, ".claude.json")
	require.Contains(t, got, `"numStartups": 42`, "非受管键必须原样保留")
	require.Contains(t, got, `"lastCost": 1.5`)
	require.Contains(t, got, `"new"`)
	require.NotContains(t, got, `"old"`, "受管键整体被替换")

	// 键顺序：numStartups 仍在 mcpServers 之前，projects 仍在最后
	require.Less(t, strings.Index(got, "numStartups"), strings.Index(got, "mcpServers"))
	require.Less(t, strings.Index(got, "mcpServers"), strings.Index(got, "projects"))
}

func TestMergeCreatesFileWhenAbsent(t *testing.T) {
	fx := newFixture(t)
	snap, blobs := keysSnap(t, `{"mcpServers":{"a":{"command":"a"}}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK, "错误：%s", ack.Error)
	require.Contains(t, fx.read(t, ".claude.json"), `"a"`)
}

// 受管键在期望内容里缺席 = 该键应当被清空为空对象，而不是保持原状。
func TestMergeClearsManagedKeyWhenAbsentInRevision(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude.json", `{"mcpServers":{"stale":{}},"other":1}`, 0o644)

	snap, blobs := keysSnap(t, `{}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK, "错误：%s", ack.Error)

	got := fx.read(t, ".claude.json")
	require.NotContains(t, got, "stale")
	require.Contains(t, got, `"other"`)
}

func TestMergeIsIdempotent(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude.json", `{"other":1,"mcpServers":{"a":{}}}`, 0o644)

	snap, blobs := keysSnap(t, `{"mcpServers":{"a":{}}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, st := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK)
	first := fx.read(t, ".claude.json")

	p2, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	ack2, _ := fx.applier.Apply(snap, p2, st)
	require.True(t, ack2.OK)
	require.Equal(t, first, fx.read(t, ".claude.json"), "内容一致时不该改动文件")
}

func TestMergeRejectsInvalidJSONOnDisk(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude.json", `{这不是 json`, 0o644)

	snap, blobs := keysSnap(t, `{"mcpServers":{}}`)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, nil)
	require.False(t, ack.OK, "磁盘上的 JSON 坏了就不能动它——改写会毁掉用户的数据")
	require.True(t, ack.RolledBack)
	require.Equal(t, `{这不是 json`, fx.read(t, ".claude.json"))
}
