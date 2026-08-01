package blobcache_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
)

// hash 口径必须与 hub 侧 blobs.Hash 逐位一致，否则缓存永远不命中且没有任何
// 报错。agent 不许 import hub，因此这里钉死同一个期望值。
func TestHashMatchesHubCanon(t *testing.T) {
	require.Equal(t,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		blobcache.Hash(nil))
}

func TestPutGetRoundTrip(t *testing.T) {
	c := blobcache.New(t.TempDir())
	content := []byte("# CLAUDE.md\n")

	h, err := c.Put(content)
	require.NoError(t, err)
	require.True(t, c.Has(h))

	got, err := c.Get(h)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestPutIsBucketed(t *testing.T) {
	dir := t.TempDir()
	c := blobcache.New(dir)
	h, err := c.Put([]byte("x"))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "blobs", h[:2], h))
}

// hub 发来的 BlobData 带着 hash，落盘前必须自己再算一遍——
// 一个被篡改的 hub 不该能往 agent 的缓存里塞任意内容。
func TestPutHashRejectsMismatch(t *testing.T) {
	c := blobcache.New(t.TempDir())
	err := c.PutHash("0000000000000000000000000000000000000000000000000000000000000000",
		[]byte("内容对不上"))
	require.Error(t, err)
	require.False(t, c.Has("0000000000000000000000000000000000000000000000000000000000000000"))
}

func TestPutHashAcceptsMatching(t *testing.T) {
	c := blobcache.New(t.TempDir())
	content := []byte("对得上")
	require.NoError(t, c.PutHash(blobcache.Hash(content), content))
	got, err := c.Get(blobcache.Hash(content))
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestMissingReturnsOnlyAbsentHashes(t *testing.T) {
	c := blobcache.New(t.TempDir())
	have, err := c.Put([]byte("有"))
	require.NoError(t, err)
	absent := blobcache.Hash([]byte("没有"))

	// 同一个 hash 重复出现只该返回一次
	require.Equal(t, []string{absent}, c.Missing([]string{have, absent, absent, have}))
	require.Empty(t, c.Missing(nil))
}

func TestGetMissingIsNotExist(t *testing.T) {
	c := blobcache.New(t.TempDir())
	_, err := c.Get(blobcache.Hash([]byte("nope")))
	require.True(t, os.IsNotExist(err))
}
