package syncer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

func collectResults(t *testing.T, r *rig) []protocol.CollectResult {
	t.Helper()
	var out []protocol.CollectResult
	for _, p := range r.out.of(protocol.KindCollectResult) {
		out = append(out, p.(protocol.CollectResult))
	}
	return out
}

func TestCollectReturnsManagedFiles(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/settings.json", `{"model":"opus"}`)
	writeManaged(t, r, ".claude/CLAUDE.md", "# 规矩")
	writeManaged(t, r, ".claude/skills/foo/SKILL.md", "skill")
	writeManaged(t, r, ".claude.json", `{"mcpServers":{}}`)

	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: "tok-1"}))

	res := collectResults(t, r)
	require.NotEmpty(t, res)
	require.True(t, res[len(res)-1].Final, "最后一批必须置 Final")

	paths := map[string]string{}
	for _, batch := range res {
		require.Equal(t, "tok-1", batch.Token, "token 随结果回传，防串批")
		for _, f := range batch.Files {
			paths[f.Path] = string(f.Content)
		}
	}
	require.Equal(t, `{"model":"opus"}`, paths[".claude/settings.json"])
	require.Equal(t, "# 规矩", paths[".claude/CLAUDE.md"])
	require.Equal(t, "skill", paths[".claude/skills/foo/SKILL.md"])
	require.Contains(t, paths, ".claude.json")
}

// 恒排除的东西绝不上传，哪怕 hub 在 manifest 里要了它（spec §3.2）。
func TestCollectRefusesAlwaysExcludedEvenIfRequested(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/.credentials.json", `{"oauth":"绝密"}`)
	writeManaged(t, r, ".claude/projects/a/session.jsonl", "会话历史")
	writeManaged(t, r, ".claude/CLAUDE.md", "正常内容")

	// 一个被篡改的 hub 试图把恒排除路径塞进 manifest
	evil := `{"version":1,"include":[
		{"path":".claude/**","mode":"tree"},
		{"path":".claude/.credentials.json","mode":"file"}
	]}`
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: []byte(evil), Token: "tok-2"}))

	res := collectResults(t, r)
	for _, batch := range res {
		for _, f := range batch.Files {
			require.NotContains(t, f.Path, ".credentials.json")
			require.NotContains(t, f.Path, "/projects/")
			require.NotContains(t, string(f.Content), "绝密")
		}
	}
}

func TestCollectReportsSkipped(t *testing.T) {
	r := newRig(t)
	writeManaged(t, r, ".claude/CLAUDE.md", "正常")
	writeManaged(t, r, ".claude/skills/foo/HUGE.md",
		strings.Repeat("x", protocol.MaxFileSize+1))

	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: "tok-3"}))

	var skipped []protocol.SkippedFile
	for _, batch := range collectResults(t, r) {
		skipped = append(skipped, batch.Skipped...)
	}
	require.Len(t, skipped, 1)
	require.Equal(t, ".claude/skills/foo/HUGE.md", skipped[0].Path)
	require.Equal(t, protocol.SkipTooLarge, skipped[0].Reason)
}

// 分批：单批累计不超过 256 KiB（spec §5.4）。
func TestCollectBatchesLargePayloads(t *testing.T) {
	r := newRig(t)
	chunk := strings.Repeat("x", 100*1024) // 100 KiB
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		writeManaged(t, r, ".claude/skills/"+name+"/SKILL.md", chunk)
	}
	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: "tok-4"}))

	res := collectResults(t, r)
	require.Greater(t, len(res), 1, "500 KiB 必须分批")
	for _, batch := range res {
		total := 0
		for _, f := range batch.Files {
			total += len(f.Content)
		}
		require.LessOrEqual(t, total, protocol.MaxBatchSize)
	}
	require.True(t, res[len(res)-1].Final)
}

// manifest 解不开时明确报回去，不发一份空结果让 hub 以为「这台机器啥都没有」。
func TestCollectReportsManifestError(t *testing.T) {
	r := newRig(t)
	r.s.Handle(envelope(t, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: []byte("不是 json"), Token: "tok-5"}))

	res := collectResults(t, r)
	require.Len(t, res, 1)
	require.True(t, res[0].Final)
	require.NotEmpty(t, res[0].Error)
	require.Empty(t, res[0].Files)
}
