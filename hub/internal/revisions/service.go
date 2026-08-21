// Package revisions 管不可变的版本：发布、checksum、版本间 diff、回滚。
//
// 历史不可变（产品 §4.2）：回滚不删任何东西，而是把旧版清单复制成一条
// 新 Revision（spec §7.2）。
package revisions

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

type Service struct {
	app   core.App
	blobs *blobs.Store
	ev    *events.Writer
	sets  *configsets.Service
}

func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service {
	return &Service{app: app, blobs: b, ev: ev, sets: configsets.NewService(app, b, ev)}
}

// Publish 把当前草稿冻结成一条新 Revision。
func (s *Service) Publish(setID, note, source string) (*core.Record, error) {
	files, err := s.sets.Draft(setID)
	if err != nil {
		return nil, err
	}
	return s.PublishFiles(setID, files, note, source)
}

// PublishFiles 用给定清单发布。收编（source=adopt）与回滚走这条。
func (s *Service) PublishFiles(setID string, files []protocol.FileEntry, note, source string) (*core.Record, error) {
	set, err := s.app.FindRecordById("config_sets", setID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 配置集 %s 不存在: %w", setID, err)
	}
	m, err := s.sets.Manifest(setID)
	if err != nil {
		return nil, err
	}
	mj, err := m.JSON()
	if err != nil {
		return nil, err
	}

	sorted := make([]protocol.FileEntry, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	refs, err := s.refsOf(sorted)
	if err != nil {
		return nil, err
	}

	c, err := s.app.FindCollectionByNameOrId("revisions")
	if err != nil {
		return nil, fmt.Errorf("revisions: 找不到 collection: %w", err)
	}

	// seq 冲突靠唯一索引兜底并重试。并发发布很罕见，但一旦发生，
	// 两条同号 revision 会让「回滚到 v2」变成歧义。
	var rev *core.Record
	for attempt := 0; attempt < 8; attempt++ {
		next, err := s.nextSeq(setID)
		if err != nil {
			return nil, err
		}
		r := core.NewRecord(c)
		r.Set("config_set", setID)
		r.Set("seq", next)
		r.Set("files", sorted)
		r.Set("manifest", json.RawMessage(mj))
		r.Set("checksum", protocol.Checksum(sorted))
		r.Set("refs", refs)
		r.Set("note", note)
		r.Set("source", source)
		if err := s.app.Save(r); err != nil {
			continue // 多半是 seq 撞了，重算再来
		}
		rev = r
		break
	}
	if rev == nil {
		return nil, fmt.Errorf("revisions: 分配 seq 失败（并发发布过多）")
	}

	set.Set("head", rev.Id)
	if err := s.app.Save(set); err != nil {
		return nil, fmt.Errorf("revisions: 更新 head: %w", err)
	}
	if err := s.ev.Write(events.KindConfigSetPublished, "", map[string]any{
		"config_set": setID, "revision": rev.Id, "seq": rev.GetInt("seq"), "source": source,
	}); err != nil {
		s.app.Logger().Warn("写 configset.published 事件失败", "error", err)
	}
	return rev, nil
}

// Rollback 把旧版清单复制成一条新 Revision，并把草稿也拉回旧版
// ——否则用户下一次发布会把刚回滚掉的内容又推上去。
func (s *Service) Rollback(setID, revisionID string) (*core.Record, error) {
	old, err := s.app.FindRecordById("revisions", revisionID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 版本 %s 不存在: %w", revisionID, err)
	}
	if old.GetString("config_set") != setID {
		return nil, fmt.Errorf("revisions: 版本 %s 不属于配置集 %s", revisionID, setID)
	}
	files, err := s.Files(revisionID)
	if err != nil {
		return nil, err
	}

	note := fmt.Sprintf("回滚自 v%d", old.GetInt("seq"))
	rev, err := s.PublishFiles(setID, files, note, "rollback")
	if err != nil {
		return nil, err
	}
	if err := s.sets.SetDraft(setID, files); err != nil {
		return nil, err
	}
	if err := s.ev.Write(events.KindConfigSetRolledBack, "", map[string]any{
		"config_set": setID, "from": revisionID, "to": rev.Id,
	}); err != nil {
		s.app.Logger().Warn("写 configset.rolled_back 事件失败", "error", err)
	}
	return rev, nil
}

func (s *Service) Files(revID string) ([]protocol.FileEntry, error) {
	r, err := s.app.FindRecordById("revisions", revID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 版本 %s 不存在: %w", revID, err)
	}
	var files []protocol.FileEntry
	if err := r.UnmarshalJSONField("files", &files); err != nil {
		return nil, fmt.Errorf("revisions: 解析 files: %w", err)
	}
	if files == nil {
		files = []protocol.FileEntry{}
	}
	return files, nil
}

// Head 返回配置集当前已发布的最新版本。尚未发布时返回错误。
func (s *Service) Head(setID string) (*core.Record, error) {
	set, err := s.app.FindRecordById("config_sets", setID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 配置集 %s 不存在: %w", setID, err)
	}
	head := set.GetString("head")
	if head == "" {
		return nil, fmt.Errorf("revisions: 配置集 %s 尚未发布任何版本", setID)
	}
	return s.app.FindRecordById("revisions", head)
}

func (s *Service) nextSeq(setID string) (int, error) {
	recs, err := s.app.FindRecordsByFilter("revisions", "config_set = {:s}", "-seq", 1, 0,
		map[string]any{"s": setID})
	if err != nil {
		return 0, fmt.Errorf("revisions: 查询最大 seq: %w", err)
	}
	if len(recs) == 0 {
		return 1, nil
	}
	return recs[0].GetInt("seq") + 1, nil
}

func (s *Service) refsOf(files []protocol.FileEntry) (configsets.Refs, error) {
	creds := map[string]bool{}
	vars := map[string]bool{}
	providerKeys := map[string]bool{}
	for _, f := range files {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			return configsets.Refs{}, fmt.Errorf("revisions: 读取 %s 的内容: %w", f.Path, err)
		}
		rs, err := protocol.Refs(content)
		if err != nil {
			return configsets.Refs{}, fmt.Errorf("revisions: %s 的占位符语法错误: %w", f.Path, err)
		}
		for _, ref := range rs {
			switch ref.Kind {
			case protocol.RefCred:
				creds[ref.Name] = true
			case protocol.RefVar:
				vars[ref.Name] = true
			case protocol.RefProvider:
				providerKeys[ref.Name] = true
			}
		}
	}
	return configsets.Refs{
		Creds:        sortedKeys(creds),
		Vars:         sortedKeys(vars),
		ProviderKeys: sortedKeys(providerKeys),
	}, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
