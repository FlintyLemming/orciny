package providers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
)

var (
	ErrNotFound     = errors.New("providers: 服务配置不存在")
	ErrInUse        = errors.New("providers: 服务配置仍被绑定")
	ErrBadAuthField = errors.New("providers: auth_field 非法")
	ErrBadBaseURL   = errors.New("providers: base_url 非法")
)

// 反查命中的档位（spec §6.3）。
const (
	MatchProvider = "provider"
	MatchPreset   = "preset"
	MatchNone     = "none"
)

// Match 是一次 base_url 反查的结果。
// Exact 为 false 表示只有 host 对得上——UI 措辞降级为「可能是」，
// 且动作仍需用户确认，不自动执行（spec §13）。
type Match struct {
	Kind         string `json:"kind"`
	Exact        bool   `json:"exact"`
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	PresetID     string `json:"preset_id,omitempty"`
	PresetName   string `json:"preset_name,omitempty"`
}

// Input 是建 / 改一条服务配置需要的全部字段。
type Input struct {
	Name       string
	Preset     string
	BaseURL    string
	AuthField  string
	Credential string // credentials 记录 id
	Models     []string
	Defaults   ModelSlots
	Note       string
}

type Store struct {
	app core.App
	ev  *events.Writer
}

func NewStore(app core.App, ev *events.Writer) *Store {
	return &Store{app: app, ev: ev}
}

func (s *Store) Get(id string) (*core.Record, error) {
	r, err := s.app.FindRecordById("providers", id)
	if err != nil || r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return r, nil
}

func (s *Store) Create(in Input) (*core.Record, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	c, err := s.app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil, fmt.Errorf("providers: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	apply(r, in)
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 创建 %s: %w", in.Name, err)
	}
	s.write(events.KindProviderCreated, r)
	return r, nil
}

// Update 改 base_url / 模型 / 凭据。
//
// **不产生新 Revision**（spec §2.2）：Provider 的当前值是活的，跟版本无关。
// 下发由调用方发一次 ConfigNotify 完成（见 configsync.NotifyProvider）。
func (s *Store) Update(id string, in Input) (*core.Record, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	apply(r, in)
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 更新 %s: %w", id, err)
	}
	s.write(events.KindProviderUpdated, r)
	return r, nil
}

// Delete 删服务配置。被任何配置集的 head_provider 或 draft_binding 指向时拒绝。
//
// 与凭据的删除保护同理（spec §5.3）：删掉一条正被指着的 Provider 会让
// 全机队在下次重注入时拿到空值。
func (s *Store) Delete(id string) error {
	r, err := s.Get(id)
	if err != nil {
		return err
	}
	setIDs, err := s.BoundBy(id)
	if err != nil {
		return err
	}
	if len(setIDs) > 0 {
		return fmt.Errorf("%w: 被 %d 个配置集的当前版本或草稿绑定", ErrInUse, len(setIDs))
	}
	if err := s.app.Delete(r); err != nil {
		return fmt.Errorf("providers: 删除 %s: %w", id, err)
	}
	s.write(events.KindProviderDeleted, r)
	return nil
}

// BoundBy 返回绑定了该 Provider 的配置集 id（head_provider 或 draft_binding）。
// UI 的「被 N 个配置集引用」与重注入的反查链都用它。
func (s *Store) BoundBy(id string) ([]string, error) {
	sets, err := s.app.FindAllRecords("config_sets")
	if err != nil {
		return nil, fmt.Errorf("providers: 扫描配置集: %w", err)
	}
	var out []string
	for _, set := range sets {
		if set.GetString("head_provider") == id {
			out = append(out, set.Id)
			continue
		}
		var b Binding
		if err := set.UnmarshalJSONField("draft_binding", &b); err == nil && b.Provider == id {
			out = append(out, set.Id)
		}
	}
	return out, nil
}

// UsingCredential 返回引用该凭据的服务配置 id。
// credentials 的删除保护与轮换重注入都靠它（spec §5.3）。
func (s *Store) UsingCredential(credID string) ([]string, error) {
	recs, err := s.app.FindRecordsByFilter("providers",
		"credential = {:c}", "name", 0, 0, map[string]any{"c": credID})
	if err != nil {
		return nil, fmt.Errorf("providers: 查询引用凭据 %s 的服务配置: %w", credID, err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Id)
	}
	return out, nil
}

// MatchBaseURL 做反查：先在已有 Provider 里精确匹配，再 host 匹配，
// 然后在内置预设里精确匹配、host 匹配，都不中返回 MatchNone（spec §6.3 / §6.4）。
func (s *Store) MatchBaseURL(raw string) (Match, error) {
	want := NormalizeURL(raw)
	wantHost := HostOf(raw)

	recs, err := s.app.FindAllRecords("providers")
	if err != nil {
		return Match{}, fmt.Errorf("providers: 扫描服务配置: %w", err)
	}
	var hostHit *core.Record
	for _, r := range recs {
		got := r.GetString("base_url")
		if NormalizeURL(got) == want {
			return Match{Kind: MatchProvider, Exact: true,
				ProviderID: r.Id, ProviderName: r.GetString("name")}, nil
		}
		if hostHit == nil && wantHost != "" && HostOf(got) == wantHost {
			hostHit = r
		}
	}
	if hostHit != nil {
		return Match{Kind: MatchProvider, Exact: false,
			ProviderID: hostHit.Id, ProviderName: hostHit.GetString("name")}, nil
	}

	var hostPreset *Preset
	for i := range presets {
		p := presets[i]
		if NormalizeURL(p.BaseURL) == want {
			return Match{Kind: MatchPreset, Exact: true,
				PresetID: p.ID, PresetName: p.Name}, nil
		}
		if hostPreset == nil && wantHost != "" && HostOf(p.BaseURL) == wantHost {
			hostPreset = &presets[i]
		}
	}
	if hostPreset != nil {
		return Match{Kind: MatchPreset, Exact: false,
			PresetID: hostPreset.ID, PresetName: hostPreset.Name}, nil
	}
	return Match{Kind: MatchNone}, nil
}

func validate(in Input) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("providers: 需要 name")
	}
	if in.AuthField != AuthToken && in.AuthField != AuthAPIKey {
		return fmt.Errorf("%w: %q（只允许 %s / %s）",
			ErrBadAuthField, in.AuthField, AuthToken, AuthAPIKey)
	}
	if HostOf(in.BaseURL) == "" {
		return fmt.Errorf("%w: %q 解析不出 host", ErrBadBaseURL, in.BaseURL)
	}
	if in.Credential == "" {
		return fmt.Errorf("providers: 需要 credential")
	}
	// 半填的四槽是配置错误：只钉主模型会让 Claude Code 拿 claude-haiku-*
	// 去打人家的 endpoint（spec §2.3）。
	if !in.Defaults.Empty() && !in.Defaults.Full() {
		return fmt.Errorf("providers: 四个模型槽必须要么全空（透传）要么全满")
	}
	return nil
}

func apply(r *core.Record, in Input) {
	models := in.Models
	if models == nil {
		models = []string{}
	}
	r.Set("name", strings.TrimSpace(in.Name))
	r.Set("preset", in.Preset)
	// 归一化后存储（spec §6.2）：反查靠它，不能把用户敲的尾斜杠带进库。
	r.Set("base_url", NormalizeURL(in.BaseURL))
	r.Set("auth_field", in.AuthField)
	r.Set("credential", in.Credential)
	r.Set("models", models)
	r.Set("defaults", in.Defaults)
	r.Set("note", in.Note)
}

// write 记事件。事件里只有名字与平台，不含 key、不含 base_url 之外的值。
func (s *Store) write(kind string, r *core.Record) {
	if s.ev == nil {
		return
	}
	if err := s.ev.Write(kind, "", map[string]any{
		"provider": r.Id, "name": r.GetString("name"), "preset": r.GetString("preset"),
	}); err != nil {
		s.app.Logger().Warn("写 "+kind+" 事件失败", "error", err)
	}
}
