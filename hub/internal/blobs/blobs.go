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
	r := core.NewRecord(c)
	r.Set("hash", h)
	r.Set("size", len(content))
	// PocketBase 的 NewFileFromBytes 拒绝空内容（cannot create an empty file）。
	// 空文件是合法草稿（UI「添加文件」先建空再编辑），size=0 且不挂 content 文件。
	if len(content) > 0 {
		f, err := filesystem.NewFileFromBytes(content, h)
		if err != nil {
			return "", fmt.Errorf("blobs: 构造文件: %w", err)
		}
		r.Set("content", f)
	}
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
		// size=0 的空 blob 故意不挂 content 文件（见 PutTx）。
		if r.GetInt("size") == 0 {
			return []byte{}, nil
		}
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

// GCOrphans 删除没有任何引用的 blob。只在删除整个配置集之后调用
// （spec §4.2：Revision 不可变不可删，平时引用计数只增）。
//
// setID 只用于日志：引用可能来自任何配置集，扫描范围永远是全库。
func (s *Store) GCOrphans(setID string) (int, error) {
	referenced := map[string]bool{}

	collect := func(collection, field string) error {
		recs, err := s.app.FindAllRecords(collection)
		if err != nil {
			return fmt.Errorf("blobs: 扫描 %s: %w", collection, err)
		}
		for _, r := range recs {
			var entries []struct {
				Hash string `json:"hash"`
			}
			// JSONField 读不了点号 key，必须走 UnmarshalJSONField。
			if err := r.UnmarshalJSONField(field, &entries); err != nil {
				continue // 空字段或形状不符：当作没有引用
			}
			for _, e := range entries {
				if e.Hash != "" {
					referenced[e.Hash] = true
				}
			}
		}
		return nil
	}
	if err := collect("revisions", "files"); err != nil {
		return 0, err
	}
	if err := collect("config_sets", "draft"); err != nil {
		return 0, err
	}

	markByID := func(id string) {
		if id == "" {
			return
		}
		if b, err := s.app.FindRecordById("blobs", id); err == nil && b != nil {
			referenced[b.GetString("hash")] = true
		}
	}

	drifts, err := s.app.FindAllRecords("drift_events")
	if err != nil {
		return 0, fmt.Errorf("blobs: 扫描 drift_events: %w", err)
	}
	for _, d := range drifts {
		markByID(d.GetString("current_blob"))
	}

	// 覆盖层的三份内容是**用户数据**：不补进来，删一个配置集就会把
	// 用户的本机保留内容当孤儿清掉（spec §8.3）。
	// 合并产物 blob 不必单独保护——它由 base + 覆盖层纯函数决定，
	// 被 GC 掉之后下一次 Snapshot 会重新算出来并 Put 回去。
	ovs, err := s.app.FindAllRecords("machine_overrides")
	if err != nil {
		return 0, fmt.Errorf("blobs: 扫描 machine_overrides: %w", err)
	}
	for _, o := range ovs {
		markByID(o.GetString("base_blob"))
		markByID(o.GetString("mine_blob"))
		markByID(o.GetString("shadowed_blob"))
	}

	all, err := s.app.FindAllRecords("blobs")
	if err != nil {
		return 0, fmt.Errorf("blobs: 扫描 blobs: %w", err)
	}
	n := 0
	for _, b := range all {
		if referenced[b.GetString("hash")] {
			continue
		}
		if err := s.app.Delete(b); err != nil {
			return n, fmt.Errorf("blobs: 删除孤儿 %s: %w", b.GetString("hash"), err)
		}
		n++
	}
	s.app.Logger().Info("清理孤儿 blob", "config_set", setID, "deleted", n)
	return n, nil
}
