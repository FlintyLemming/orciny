package manifest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func rels(exp manifest.Expansion) []string {
	out := make([]string, 0, len(exp.Files))
	for _, f := range exp.Files {
		out = append(out, f.Rel)
	}
	return out
}

func TestExpandThreeModes(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/settings.json", `{"model":"opus"}`)
	write(t, root, ".claude/CLAUDE.md", "# 规矩")
	write(t, root, ".claude/skills/foo/SKILL.md", "skill")
	write(t, root, ".claude/skills/bar/nested/deep.md", "deep")
	write(t, root, ".claude.json", `{"mcpServers":{}}`)
	// keybindings.json 不存在：不该报错，也不该进清单
	// agents/ 与 commands/ 不存在：同上

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{
		".claude.json",
		".claude/CLAUDE.md",
		".claude/settings.json",
		".claude/skills/bar/nested/deep.md",
		".claude/skills/foo/SKILL.md",
	}, rels(exp))

	byRel := map[string]manifest.Entry{}
	for _, f := range exp.Files {
		byRel[f.Rel] = f
	}
	require.Equal(t, manifest.ModeKeys, byRel[".claude.json"].Inc.Mode)
	require.Equal(t, manifest.ModeFile, byRel[".claude/settings.json"].Inc.Mode)
	require.Equal(t, manifest.ModeTree, byRel[".claude/skills/foo/SKILL.md"].Inc.Mode)

	evalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(evalRoot, ".claude/CLAUDE.md"), byRel[".claude/CLAUDE.md"].Abs)
}

// tree 模式含新增文件——这是收编能成立的支点（spec §3.1）。
func TestExpandTreePicksUpNewFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "a")
	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))

	write(t, root, ".claude/skills/新技能/SKILL.md", "b")
	exp, err = manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Len(t, exp.Files, 2)
}

func TestExpandSkipsAlwaysExcluded(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "ok")
	write(t, root, ".claude/skills/foo/node_modules/x/index.js", "nope")
	write(t, root, ".claude/skills/foo/.git/config", "nope")

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))

	var reasons []string
	for _, s := range exp.Skipped {
		reasons = append(reasons, s.Reason)
	}
	require.NotEmpty(t, exp.Skipped)
	for _, r := range reasons {
		require.Equal(t, protocol.SkipAlwaysExcluded, r)
	}
}

func TestExpandSkipsOversizeFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "ok")
	big := filepath.Join(root, ".claude/skills/foo/HUGE.md")
	require.NoError(t, os.WriteFile(big, make([]byte, protocol.MaxFileSize+1), 0o644))

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))
	require.Len(t, exp.Skipped, 1)
	require.Equal(t, ".claude/skills/foo/HUGE.md", exp.Skipped[0].Rel)
	require.Equal(t, protocol.SkipTooLarge, exp.Skipped[0].Reason)
}

func TestExpandSkipsNonRegularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接与 FIFO 需要特权")
	}
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "ok")
	require.NoError(t, os.Symlink(
		filepath.Join(root, ".claude/skills/foo/SKILL.md"),
		filepath.Join(root, ".claude/skills/foo/link.md")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, ".claude/skills/foo/pipe"), 0o644))

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))
	require.Len(t, exp.Skipped, 2)
	for _, s := range exp.Skipped {
		require.Equal(t, protocol.SkipNotRegular, s.Reason)
	}
}

func TestExpandIgnoresMissingPaths(t *testing.T) {
	root := t.TempDir()
	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Empty(t, exp.Files)
	require.Empty(t, exp.Skipped, "不存在不算跳过，只是没有")
}
