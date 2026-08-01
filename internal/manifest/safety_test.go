package manifest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
)

func TestSafeRelPath(t *testing.T) {
	for _, ok := range []string{".claude/settings.json", ".claude.json", "a/b/c"} {
		require.NoError(t, manifest.SafeRelPath(ok), "%q 应当合法", ok)
	}
	for _, bad := range []string{
		"", "/etc/passwd", "../escape", "a/../../b", "a/..", "./a/../../b",
		"C:\\Windows\\system32",
	} {
		require.Error(t, manifest.SafeRelPath(bad), "%q 必须被拒", bad)
	}
}

func TestResolveUnderReturnsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	evalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	abs, err := manifest.ResolveUnder(root, ".claude/settings.json")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(evalRoot, ".claude/settings.json"), abs)
}

// 符号链接逃逸：EvalSymlinks 之后必须再判一次（spec §3.3）。
func TestResolveUnderRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接需要特权")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := manifest.ResolveUnder(root, "link/secret")
	require.Error(t, err, "顺着符号链接跑出 root 必须被拒")
}

// 不存在的文件也要能算出路径：apply 的 create 动作正是对不存在的路径落盘。
func TestResolveUnderAllowsMissingFile(t *testing.T) {
	root := t.TempDir()
	evalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	abs, err := manifest.ResolveUnder(root, ".claude/agents/new.md")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(evalRoot, ".claude/agents/new.md"), abs)
}
