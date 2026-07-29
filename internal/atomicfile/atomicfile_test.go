package atomicfile_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

func TestWriteCreatesFileWithPerm(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.key")

	require.NoError(t, atomicfile.Write(p, []byte("secret"), 0o600))

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "secret", string(b))

	if runtime.GOOS != "windows" {
		st, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
}

func TestWriteReplacesExistingContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yml")
	require.NoError(t, atomicfile.Write(p, []byte("old-and-long"), 0o644))
	require.NoError(t, atomicfile.Write(p, []byte("new"), 0o644))

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "new", string(b), "必须整体替换，不能留下旧内容的尾巴")
}

func TestWriteLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, atomicfile.Write(filepath.Join(dir, "a.yml"), []byte("x"), 0o644))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "临时文件必须已被 rename 掉")
}

func TestWriteCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "identity", "agent.key")
	require.NoError(t, atomicfile.Write(p, []byte("k"), 0o600))
	require.FileExists(t, p)
}
