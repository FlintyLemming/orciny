// Package blobs 是内容寻址存储：一份内容全库只存一次，按 sha256 索引
// （spec §7.1）。
//
// 这层刻意薄：Revision 不可变、不可删，引用计数天然只增，因此不需要计数
// 字段，只有删配置集时才扫一次孤儿（spec §4.2）。
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

var ErrNotFound = errors.New("blobs: 内容不存在")

// Hash 返回内容的 sha256（hex）。这个口径进了 revision.files 与 wire，
// 不得更改。
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

type Store struct {
	app core.App
}

func New(app core.App) *Store { return &Store{app: app} }

// Put 写入内容并返回其 hash。已存在则直接返回，不重复落盘。
func (s *Store) Put(content []byte) (string, error) { return s.PutTx(s.app, content) }

// PutTx 在给定的 app（可能是事务）上写入。在事务里务必用它，
// 否则事务回滚了 blob 还留着。
func (s *Store) PutTx(txApp core.App, content []byte) (string, error) {
	h := Hash(content)

	if r, err := txApp.FindFirstRecordByData("blobs", "hash", h); err == nil && r != nil {
		return h, nil
	}

	c, err := txApp.FindCollectionByNameOrId("blobs")
	if err != nil {
		return "", fmt.Errorf("blobs: 找不到 collection: %w", err)
	}
	f, err := filesystem.NewFileFromBytes(content, h)
	if err != nil {
		return "", fmt.Errorf("blobs: 构造文件: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("hash", h)
	r.Set("size", len(content))
	r.Set("content", f)
	if err := txApp.Save(r); err != nil {
		// 并发写同一 hash 时唯一索引会让后来者失败。此时对方已经写成功，
		// 结果与我们想要的完全一致，因此当成成功。
		if existing, e := txApp.FindFirstRecordByData("blobs", "hash", h); e == nil && existing != nil {
			return h, nil
		}
		return "", fmt.Errorf("blobs: 写入 %s: %w", h, err)
	}
	return h, nil
}

// Get 按 hash 取内容。
func (s *Store) Get(hash string) ([]byte, error) {
	r, err := s.app.FindFirstRecordByData("blobs", "hash", hash)
	if err != nil || r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	name := r.GetString("content")
	if name == "" {
		return nil, fmt.Errorf("%w: %s（记录在但文件为空）", ErrNotFound, hash)
	}

	fsys, err := s.app.NewFilesystem()
	if err != nil {
		return nil, fmt.Errorf("blobs: 打开存储: %w", err)
	}
	defer fsys.Close()

	rd, err := fsys.GetReader(r.BaseFilesPath() + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("blobs: 读取 %s: %w", hash, err)
	}
	defer rd.Close()

	b, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("blobs: 读取 %s: %w", hash, err)
	}
	return b, nil
}

func (s *Store) Has(hash string) (bool, error) {
	r, err := s.app.FindFirstRecordByData("blobs", "hash", hash)
	if err != nil || r == nil {
		return false, nil
	}
	return true, nil
}
