// Package blobcache 是 ~/.orciny/blobs/ 下的本地内容缓存。
//
// 有了它，同一份内容只从 hub 拉一次；hub 离线时也能重渲染基线与回滚。
package blobcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// DirName 是缓存在 agent 目录下的子目录名。
const DirName = "blobs"

// Hash 与 hub 侧 blobs.Hash 同源：sha256 的 hex。
// 口径不一致会让缓存永远不命中，且没有任何报错——因此有一条钉死期望值的测试。
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

type Cache struct{ dir string }

// New 返回挂在 agent 目录 dir 下的缓存。
func New(dir string) *Cache { return &Cache{dir: filepath.Join(dir, DirName)} }

// path 分两级：一个目录里堆几千个文件对某些文件系统不友好。
func (c *Cache) path(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(c.dir, "_", hash)
	}
	return filepath.Join(c.dir, hash[:2], hash)
}

func (c *Cache) Has(hash string) bool {
	info, err := os.Stat(c.path(hash))
	return err == nil && info.Mode().IsRegular()
}

func (c *Cache) Get(hash string) ([]byte, error) {
	return os.ReadFile(c.path(hash)) // 保留 os.ErrNotExist
}

func (c *Cache) Put(content []byte) (string, error) {
	h := Hash(content)
	if err := c.write(h, content); err != nil {
		return "", err
	}
	return h, nil
}

// PutHash 落盘一份声称是 hash 的内容，落盘前自己再算一遍。
//
// 这是 agent 少数几处能自我保护的地方：hub 发来的 BlobData 带着 hash，
// 若不校验，一个被篡改的 hub 就能往缓存里塞任意内容，而缓存正是
// 「hub 离线时也能自愈」所依赖的基线。
func (c *Cache) PutHash(hash string, content []byte) error {
	if got := Hash(content); got != hash {
		return fmt.Errorf("blobcache: 内容与 hash 不符（声称 %s，实为 %s）", hash, got)
	}
	return c.write(hash, content)
}

func (c *Cache) write(hash string, content []byte) error {
	// 0600：缓存里躺的是**渲染前**的内容（占位符形态），仍然按敏感对待。
	if err := atomicfile.Write(c.path(hash), content, 0o600); err != nil {
		return fmt.Errorf("blobcache: 写入 %s: %w", hash, err)
	}
	return nil
}

// Missing 返回本地没有的 hash，去重并保持首次出现的顺序。
func (c *Cache) Missing(hashes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hashes {
		if seen[h] || c.Has(h) {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}
