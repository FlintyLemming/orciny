// Package configsets 管配置集本体：草稿、manifest、发布期校验、指派。
//
// 草稿与发布共用一套形状（spec §4.2）：config_sets.draft 与 revisions.files
// 都是 []protocol.FileEntry，于是「草稿 vs head」与「v3 vs v1」是同一个
// diff 函数，前端也只有一种数据形状要处理。
package configsets

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type Service struct {
	app   core.App
	blobs *blobs.Store
	ev    *events.Writer
	provs *providers.Store
}

// NewService 的签名不变，内部自建 providers.Store——与 revisions.NewService
// 内部自建 configsets.Service 是同一个手法，装配点不用跟着改。
func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service {
	return &Service{app: app, blobs: b, ev: ev, provs: providers.NewStore(app, ev)}
}

// Refs 是 config_sets.draft_refs 与 revisions.refs 的形状。
//
// ProviderKeys 记的是**哪几个 {{provider.*}} 内置名被引用**（形如
// ["auth_token","base_url"]），不是绑定指向哪条 Provider——后者在
// head_provider / binding 里。下发时靠它裁剪，不读 blob 内容（M1.5 spec §2.2）。
type Refs struct {
	Creds        []string `json:"creds"`
	Vars         []string `json:"vars"`
	ProviderKeys []string `json:"provider_keys"`
}

func (s *Service) Create(name, note string) (*core.Record, error) {
	c, err := s.app.FindCollectionByNameOrId("config_sets")
	if err != nil {
		return nil, fmt.Errorf("configsets: 找不到 collection: %w", err)
	}
	mj, err := manifest.Default().JSON()
	if err != nil {
		return nil, err
	}
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("note", note)
	r.Set("manifest", json.RawMessage(mj))
	r.Set("draft", []protocol.FileEntry{})
	r.Set("draft_refs", Refs{Creds: []string{}, Vars: []string{}, ProviderKeys: []string{}})
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("configsets: 创建 %s: %w", name, err)
	}
	return r, nil
}

func (s *Service) record(setID string) (*core.Record, error) {
	r, err := s.app.FindRecordById("config_sets", setID)
	if err != nil {
		return nil, fmt.Errorf("configsets: 配置集 %s 不存在: %w", setID, err)
	}
	return r, nil
}

func (s *Service) Draft(setID string) ([]protocol.FileEntry, error) {
	r, err := s.record(setID)
	if err != nil {
		return nil, err
	}
	return decodeFiles(r, "draft")
}

// SetDraft 整体替换草稿清单。导入向导与收编的组装路径用它。
func (s *Service) SetDraft(setID string, files []protocol.FileEntry) error {
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	return s.saveDraft(r, files)
}

// SetDraftFile 写一个草稿文件：内容落 blob，清单里更新或新增一条。
func (s *Service) SetDraftFile(setID, path string, content []byte, mode uint32, keys []string) (protocol.FileEntry, error) {
	var zero protocol.FileEntry
	if err := manifest.SafeRelPath(path); err != nil {
		return zero, fmt.Errorf("configsets: 路径 %s 非法: %w", path, err)
	}
	// 恒排除双侧各判一次：hub 侧防止「发布了一个包含 .credentials.json
	// 的版本」（spec §3.2）。
	if manifest.IsAlwaysExcluded(path) {
		return zero, fmt.Errorf("configsets: %s 属于恒排除路径，不可纳管", path)
	}
	if len(content) > protocol.MaxFileSize {
		return zero, fmt.Errorf("configsets: %s 有 %d 字节，超过单文件上限 512 KiB",
			path, len(content))
	}

	r, err := s.record(setID)
	if err != nil {
		return zero, err
	}
	hash, err := s.blobs.Put(content)
	if err != nil {
		return zero, err
	}
	entry := protocol.FileEntry{
		Path: path, Hash: hash, Size: uint32(len(content)), Mode: mode, Keys: keys,
	}

	files, err := decodeFiles(r, "draft")
	if err != nil {
		return zero, err
	}
	replaced := false
	for i := range files {
		if files[i].Path == path {
			files[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		files = append(files, entry)
	}
	if err := s.saveDraft(r, files); err != nil {
		return zero, err
	}
	return entry, nil
}

func (s *Service) RemoveDraftFile(setID, path string) error {
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	files, err := decodeFiles(r, "draft")
	if err != nil {
		return err
	}
	out := files[:0]
	for _, f := range files {
		if f.Path != path {
			out = append(out, f)
		}
	}
	return s.saveDraft(r, out)
}

// saveDraft 排序、重算引用集合、落库。引用集合是凭据删除保护的依据
// （spec §6.5），因此每次改草稿都要重算，不能懒更新。
func (s *Service) saveDraft(r *core.Record, files []protocol.FileEntry) error {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	refs, err := s.collectRefs(files)
	if err != nil {
		return err
	}
	r.Set("draft", files)
	r.Set("draft_refs", refs)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("configsets: 保存草稿: %w", err)
	}
	return nil
}

// collectRefs 读回每个 blob 的内容并提取占位符引用，去重后按名字升序。
// 清单项可能来自外部组装（导入、收编），内容尚未落库时跳过该文件的引用提取——
// 真正的把关在 Validate。
func (s *Service) collectRefs(files []protocol.FileEntry) (Refs, error) {
	creds := map[string]bool{}
	vars := map[string]bool{}
	providerKeys := map[string]bool{}
	for _, f := range files {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			continue
		}
		rs, err := protocol.Refs(content)
		if err != nil {
			return Refs{}, fmt.Errorf("configsets: %s 的占位符语法错误: %w", f.Path, err)
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
			// machine.* 是内置值，不进引用集合。
		}
	}
	return Refs{
		Creds:        sortedKeys(creds),
		Vars:         sortedKeys(vars),
		ProviderKeys: sortedKeys(providerKeys),
	}, nil
}

func (s *Service) Manifest(setID string) (manifest.Manifest, error) {
	r, err := s.record(setID)
	if err != nil {
		return manifest.Manifest{}, err
	}
	raw := r.GetString("manifest")
	if raw == "" {
		return manifest.Default(), nil
	}
	return manifest.Parse([]byte(raw))
}

func (s *Service) SetManifest(setID string, m manifest.Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	b, err := m.JSON()
	if err != nil {
		return err
	}
	r.Set("manifest", json.RawMessage(b))
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("configsets: 保存 manifest: %w", err)
	}
	return nil
}

func decodeFiles(r *core.Record, field string) ([]protocol.FileEntry, error) {
	var files []protocol.FileEntry
	// JSONField 读不了点号 key，也不能用 GetString 之后自己 Unmarshal 之外的路子。
	if err := r.UnmarshalJSONField(field, &files); err != nil {
		return nil, fmt.Errorf("configsets: 解析 %s: %w", field, err)
	}
	if files == nil {
		files = []protocol.FileEntry{}
	}
	return files, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
