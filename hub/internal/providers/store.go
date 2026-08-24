package providers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
)

var (
	ErrNotFound     = errors.New("providers: 服务配置不存在")
	ErrInUse        = errors.New("providers: 服务配置仍被绑定")
	ErrBadAuthField = errors.New("providers: auth_field 非法")
	ErrBadBaseURL   = errors.New("providers: base_url 非法")
	// ErrNoKey：某个配置了 base_url 的端点两级都解不出 key。
	// 与「四槽要么全空要么全满」同级的配置错误（spec §2.3）。
	ErrNoKey = errors.New("providers: 端点没有可用的 key")
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

// ClaudeEndpointInput 的 Key 是三态（spec §5.2「留空则不修改，填写即替换」）：
//
//	nil  = 不修改（编辑态密码框留空）
//	""   = 清空（端点级清空即回落平台级）
//	非空 = 替换
type ClaudeEndpointInput struct {
	BaseURL   string
	AuthField string
	Models    []ClaudeModel
	Key       *string
	Defaults  ModelSlots
}

// OpenAIEndpointInput 的 Key 同三态。
type OpenAIEndpointInput struct {
	BaseURL      string
	AuthField    string
	Models       []string
	Key          *string
	DefaultModel string
}

// Input 是建 / 改一条服务配置需要的全部字段。
type Input struct {
	Name   string
	Preset string
	Note   string
	Key    *string // 平台级，三态同 ClaudeEndpointInput.Key
	Claude ClaudeEndpointInput
	OpenAI OpenAIEndpointInput
}


type Store struct {
	app core.App
	key []byte
	ev  *events.Writer
}

func NewStore(app core.App, key []byte, ev *events.Writer) *Store {
	return &Store{app: app, key: key, ev: ev}
}

func (s *Store) Get(id string) (*core.Record, error) {
	r, err := s.app.FindRecordById("providers", id)
	if err != nil || r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return r, nil
}

func (s *Store) Create(in Input) (*core.Record, error) {
	c, err := s.app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil, fmt.Errorf("providers: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	if err := s.applyAndValidate(r, in); err != nil {
		return nil, err
	}
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 创建 %s: %w", in.Name, err)
	}
	s.write(events.KindProviderCreated, r)
	return r, nil
}

// Update 改任一端点的 base_url / 模型 / key。
//
// **不产生新 Revision**（M1.5 spec §2.2）：Provider 的当前值是活的，跟版本无关。
// 下发由调用方发一次 ConfigNotify 完成（见 configsync.NotifyProvider）。
func (s *Store) Update(id string, in Input) (*core.Record, error) {
	r, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if err := s.applyAndValidate(r, in); err != nil {
		return nil, err
	}
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("providers: 更新 %s: %w", id, err)
	}
	s.write(events.KindProviderUpdated, r)
	return r, nil
}

// Delete 删服务配置。被任何配置集的 head_provider 或 draft_binding 指向时拒绝。
//
// 这是本期唯一的删除保护：key 现在就在 provider 里，删 provider 就是删 key，
// 没有第二个实体可以被误删（spec §2.7）。删掉一条正被指着的 Provider 会让
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

// MatchBaseURL 做反查：先在已有 Provider 的 **claude 端点**里精确匹配，
// 再 host 匹配，然后在内置预设的 claude 端点里精确匹配、host 匹配，
// 都不中返回 MatchNone（M1.5 spec §6.3 / §6.4）。
//
// **只扫 claude 端点**（M1.6 spec §1.3）：反查链的唯一消费者是绑定漂移，
// 而漂移源只有 .claude/**。让 openai 端点参与 host 匹配还会让「可能是」
// 那一档失去信息量——同一家平台两个口的 host 本来就相同。
func (s *Store) MatchBaseURL(raw string) (Match, error) {
	want := NormalizeURL(raw)
	wantHost := HostOf(raw)

	recs, err := s.app.FindAllRecords("providers")
	if err != nil {
		return Match{}, fmt.Errorf("providers: 扫描服务配置: %w", err)
	}
	var hostHit *core.Record
	for _, r := range recs {
		got := ClaudeOf(r).BaseURL
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
		if NormalizeURL(p.Claude.BaseURL) == want {
			return Match{Kind: MatchPreset, Exact: true,
				PresetID: p.ID, PresetName: p.Name}, nil
		}
		if hostPreset == nil && wantHost != "" && HostOf(p.Claude.BaseURL) == wantHost {
			hostPreset = &presets[i]
		}
	}
	if hostPreset != nil {
		return Match{Kind: MatchPreset, Exact: false,
			PresetID: hostPreset.ID, PresetName: hostPreset.Name}, nil
	}
	return Match{Kind: MatchNone}, nil
}

// applyAndValidate 把 Input 写进记录，再对**记录上的实际值**做校验。
//
// 顺序是「先写后校验」而不是反过来：key 的三态里有「不修改」这一档，
// 「这个端点有没有 key」只有在合并了记录上的旧密文之后才知道。
func (s *Store) applyAndValidate(r *core.Record, in Input) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("providers: 需要 name")
	}
	if err := validateClaudeEndpointInput(in.Claude); err != nil {
		return err
	}
	if err := validateOpenAIEndpointInput(in.OpenAI); err != nil {
		return err
	}

	r.Set("name", strings.TrimSpace(in.Name))
	r.Set("preset", in.Preset)
	r.Set("note", in.Note)

	if err := s.applyKey(r, "key_cipher", "key_last4", in.Key); err != nil {
		return err
	}
	if err := s.applyKey(r, "claude_key_cipher", "", in.Claude.Key); err != nil {
		return err
	}
	if err := s.applyKey(r, "openai_key_cipher", "", in.OpenAI.Key); err != nil {
		return err
	}

	claude := ClaudeEndpoint{
		Endpoint: Endpoint{
			// 归一化后存储（M1.5 spec §6.2）：反查靠它，
			// 不能把用户敲的尾斜杠带进库。
			BaseURL:   NormalizeURL(in.Claude.BaseURL),
			AuthField: in.Claude.AuthField,
		},
		Models:   orEmptyClaude(in.Claude.Models),
		Defaults: in.Claude.Defaults,
	}
	openai := OpenAIEndpoint{
		Endpoint: Endpoint{
			BaseURL:   NormalizeURL(in.OpenAI.BaseURL),
			AuthField: in.OpenAI.AuthField,
		},
		Models:       orEmptyStrings(in.OpenAI.Models),
		DefaultModel: in.OpenAI.DefaultModel,
	}
	if openai.AuthField == "" {
		openai.AuthField = DefaultOpenAIAuthField
	}
	r.Set("claude", claude)
	r.Set("openai", openai)

	// 末四位跟着**实际生效的那把 key** 走，两级取值之后才算得出来。
	return s.stampLast4(r)
}

// applyKey 按三态写一处密文。last4Field 为空表示这一处的末四位存在端点 JSON 里
// （由 stampLast4 统一回填），不需要单独的顶层字段。
func (s *Store) applyKey(r *core.Record, cipherField, last4Field string, v *string) error {
	if v == nil {
		return nil // 不修改
	}
	if *v == "" {
		r.Set(cipherField, "")
		if last4Field != "" {
			r.Set(last4Field, "")
		}
		return nil
	}
	if len(*v) < secretbox.MinValueLen {
		return fmt.Errorf("%w: 至少 %d 个字符", secretbox.ErrShortValue, secretbox.MinValueLen)
	}
	enc, err := secretbox.Encrypt(s.key, *v)
	if err != nil {
		return err
	}
	r.Set(cipherField, enc)
	if last4Field != "" {
		r.Set(last4Field, secretbox.Last4(*v))
	}
	return nil
}

// stampLast4 给两个端点回填「实际生效的那把 key 的末四位」，
// 顺带把「配了 base_url 却解不出 key」这条校验做掉。
func (s *Store) stampLast4(r *core.Record) error {
	for _, ep := range []string{EndpointClaude, EndpointOpenAI} {
		configured := false
		switch ep {
		case EndpointClaude:
			configured = ClaudeOf(r).Configured()
		case EndpointOpenAI:
			configured = OpenAIOf(r).Configured()
		}

		v, err := s.Key(r, ep)
		if err != nil {
			if !configured {
				// 没配的端点没有 key 很正常，末四位清空。
				if err := setEndpointLast4(r, ep, ""); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("%w：%s 端点配了 base_url，"+
				"但平台级与端点级都没有 key", ErrNoKey, ep)
		}
		if err := setEndpointLast4(r, ep, secretbox.Last4(v)); err != nil {
			return err
		}
	}
	return nil
}

func setEndpointLast4(r *core.Record, ep, last4 string) error {
	switch ep {
	case EndpointClaude:
		e := ClaudeOf(r)
		e.KeyLast4 = last4
		r.Set(EndpointClaude, e)
	case EndpointOpenAI:
		e := OpenAIOf(r)
		e.KeyLast4 = last4
		r.Set(EndpointOpenAI, e)
	default:
		return fmt.Errorf("providers: 未知端点 %q", ep)
	}
	return nil
}

// Key 按两级取值解出某端点实际使用的 key（spec §2.3）：
// 端点级密文非空则用它，否则回落平台级。
//
// 为什么允许覆盖而不是强制平台级一把：智谱 / 火山 / Kimi 的两个协议口确实
// 共用同一把 key，但中转类平台不一定；而「需要两把 key 就建两条 provider」
// 会把同一家平台重新拆成两条记录，正好抵消掉本期要解决的问题。
//
// 为什么不做成「只有端点级」：那样共用一把 key 的平台要粘贴两遍、轮换时
// 要改两处——这是本期最常见的情形，不该为边缘情形付代价。
func (s *Store) Key(r *core.Record, endpoint string) (string, error) {
	var field string
	switch endpoint {
	case EndpointClaude:
		field = "claude_key_cipher"
	case EndpointOpenAI:
		field = "openai_key_cipher"
	default:
		return "", fmt.Errorf("providers: 未知端点 %q", endpoint)
	}
	if c := r.GetString(field); c != "" {
		return secretbox.Decrypt(s.key, c)
	}
	if c := r.GetString("key_cipher"); c != "" {
		return secretbox.Decrypt(s.key, c)
	}
	return "", fmt.Errorf("%w: %s 的 %s 端点", ErrNoKey, r.GetString("name"), endpoint)
}

// SetEndpointKey 把一把明文 key 写进某端点，并回填末四位。
//
// 与 Update 走同一套加密与长度校验，但**不碰其余字段**：抽取的语义是
// 「给这个端点补上 key」，不该顺手把用户在 UI 上没动过的 base_url 改掉。
func (s *Store) SetEndpointKey(r *core.Record, endpoint, value string) error {
	if len(value) < secretbox.MinValueLen {
		return fmt.Errorf("%w: 至少 %d 个字符", secretbox.ErrShortValue, secretbox.MinValueLen)
	}
	var field string
	switch endpoint {
	case EndpointClaude:
		field = "claude_key_cipher"
	case EndpointOpenAI:
		field = "openai_key_cipher"
	default:
		return fmt.Errorf("providers: 未知端点 %q", endpoint)
	}
	enc, err := secretbox.Encrypt(s.key, value)
	if err != nil {
		return err
	}
	r.Set(field, enc)
	if err := setEndpointLast4(r, endpoint, secretbox.Last4(value)); err != nil {
		return err
	}
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("providers: 写入 %s 端点的 key: %w", endpoint, err)
	}
	s.write(events.KindProviderUpdated, r)
	return nil
}

// VerifyAll 在启动时逐条解密自检（spec §2.6）。
//
// **这条不能丢。** 它防的是「从备份恢复到新机器时忘了带 secret.key」——
// 最可能的翻车场景。宁可开不了机，也不能让用户以为一切正常，然后把空值
// 下发到全机队。
func (s *Store) VerifyAll() error {
	recs, err := s.app.FindAllRecords("providers")
	if err != nil {
		return fmt.Errorf("providers: 读取服务配置: %w", err)
	}
	for _, r := range recs {
		for _, pair := range []struct{ field, label string }{
			{"key_cipher", "平台级"},
			{"claude_key_cipher", "claude 端点"},
			{"openai_key_cipher", "openai 端点"},
		} {
			c := r.GetString(pair.field)
			if c == "" {
				continue
			}
			if _, err := secretbox.Decrypt(s.key, c); err != nil {
				return fmt.Errorf("providers: 服务配置 %q 的 %s 密钥解密失败——主密钥不匹配。"+
					"若是从备份恢复，请把原机器的 %s 或 %s 一并带过来: %w",
					r.GetString("name"), pair.label,
					secretbox.KeyFileName, secretbox.EnvKeyName, err)
			}
		}
	}
	return nil
}

// validateClaudeEndpointInput 只看 Claude 端点输入本身能不能自洽。
func validateClaudeEndpointInput(in ClaudeEndpointInput) error {
	if strings.TrimSpace(in.BaseURL) == "" {
		return nil // 未配置的端点不校验其余字段
	}
	if HostOf(in.BaseURL) == "" {
		return fmt.Errorf("%w: claude 端点的 %q 解析不出 host", ErrBadBaseURL, in.BaseURL)
	}
	// claude 端点的 auth_field 是二选一枚举（M1.5 spec §3.3）：
	// 它是 Claude Code 的 settings.json env 键名问题。
	if in.AuthField != AuthToken && in.AuthField != AuthAPIKey {
		return fmt.Errorf("%w: %q（只允许 %s / %s）",
			ErrBadAuthField, in.AuthField, AuthToken, AuthAPIKey)
	}
	// 半填的四槽是配置错误：只钉主模型会让 Claude Code 拿 claude-haiku-*
	// 去打人家的 endpoint（M1.5 spec §2.3）。
	if !in.Defaults.Empty() && !in.Defaults.Full() {
		return fmt.Errorf("providers: 四个模型槽必须要么全空（透传）要么全满")
	}
	return nil
}

// validateOpenAIEndpointInput 只看 OpenAI 端点输入本身能不能自洽。
func validateOpenAIEndpointInput(in OpenAIEndpointInput) error {
	if strings.TrimSpace(in.BaseURL) == "" {
		return nil
	}
	if HostOf(in.BaseURL) == "" {
		return fmt.Errorf("%w: openai 端点的 %q 解析不出 host", ErrBadBaseURL, in.BaseURL)
	}
	// openai 端点的 auth_field 是自由文本，不校验（spec §2.4）。
	return nil
}

func orEmptyClaude(xs []ClaudeModel) []ClaudeModel {
	if xs == nil {
		return []ClaudeModel{}
	}
	return xs
}

func orEmptyStrings(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
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
