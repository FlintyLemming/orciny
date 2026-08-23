package hub

import (
	"fmt"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// ProviderInput 是 providers.Input 的公开别名。
//
// internal/testsupport 够不到 hub/internal/*（Go 的 internal 规则），
// 因此凡是要跨出 hub 包的类型都在这里重新导出。用别名而不是新结构体，
// 避免两份字段要同步。
type ProviderInput = providers.Input

// ProviderBinding 同理：配置集的服务绑定要能从 hub 外部构造。
type ProviderBinding = providers.Binding

// ProviderModelSlots 是四个模型槽。
type ProviderModelSlots = providers.ModelSlots

// CreateProvider 新建 AI 服务配置。
//
// 新建**不需要**重注入：还没有任何配置集绑着它。
func (h *Hub) CreateProvider(in providers.Input) (string, error) {
	rec, err := h.provs.Create(in)
	if err != nil {
		return "", err
	}
	return rec.Id, nil
}

// UpdateProvider 改 base_url / 模型 / 凭据，然后立即重注入全机队。
//
// **不产生新 Revision**（M1.5 spec §2.2）。UI 上这件事必须对用户可见——
// 保存前提示「这会立即重注入到 N 台机器，不产生新版本」（spec §8.1）。
func (h *Hub) UpdateProvider(id string, in providers.Input) error {
	if _, err := h.provs.Update(id, in); err != nil {
		return err
	}
	return h.sync.NotifyProvider(id)
}

// DeleteProvider 删除服务配置。被任何配置集绑定时拒绝。
func (h *Hub) DeleteProvider(id string) error {
	return h.provs.Delete(id)
}

// SetBinding 设置或清空配置集的草稿绑定。b 为 nil 即解绑。
//
// 只改草稿：换绑定要产生新 Revision，走正常的发布流程（spec §2.2 / §8.2）。
func (h *Hub) SetBinding(setID string, b *providers.Binding) error {
	return h.sets.SetDraftBinding(setID, b)
}

// FixAuthField 把 settings.json 里承载 API key 的 env 键名改成绑定的
// auth_field（spec §7 第 3 条）。改动落在草稿上，用户在 diff 里看得见。
func (h *Hub) FixAuthField(setID string) error {
	return h.sets.FixAuthField(setID)
}

// ProviderPresets 返回内置预设表。编译期常量，只读（spec §2.3）。
func (h *Hub) ProviderPresets() []providers.Preset {
	return providers.Presets()
}

// MatchBindingDrift 对一条绑定漂移做反查三档（M1.5 spec §6.3）。
func (h *Hub) MatchBindingDrift(eventID string) (drift.BindingMatch, error) {
	return h.drift.MatchBinding(eventID)
}

// RebindFromDrift 把漂移所属配置集的绑定改成指定 Provider 并发布新版本，
// 返回新 revision id。
func (h *Hub) RebindFromDrift(eventID, providerID string) (string, error) {
	rev, err := h.drift.Rebind(eventID, providerID)
	if err != nil {
		return "", err
	}
	return rev.Id, nil
}

// CreateProviderFromDrift 把漂移内容里 location 处的值取出来内联进
// providers.Input，然后一次建成（M1.5 spec §6.3 第二档 + M1.6 spec §5.5）。
//
// 一次成型而不是「先建再补」：providers.Store 会校验「配了 base_url 的端点
// 必须能解出一把 key」，先建会被它挡住；而放宽那条校验去迁就一个向导，
// 是拿数据面的正确性换交互的便利。顺带也少了 M1.5 里「两步之间失败留下一条
// 孤儿凭据」的中间态。
func (h *Hub) CreateProviderFromDrift(
	eventID, location, endpoint string, in providers.Input,
) (string, error) {
	if location != "" {
		value, err := h.drift.DriftValue(eventID, location)
		if err != nil {
			return "", err
		}
		switch endpoint {
		case providers.EndpointClaude:
			in.Claude.Key = &value
		case providers.EndpointOpenAI:
			in.OpenAI.Key = &value
		default:
			return "", fmt.Errorf("未知端点 %q", endpoint)
		}
	}
	return h.CreateProvider(in)
}
