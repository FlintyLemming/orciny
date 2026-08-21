package configsets

import (
	"fmt"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// DraftBinding 读草稿绑定。未绑定返回 (nil, nil)——「没绑」不是错误。
func (s *Service) DraftBinding(setID string) (*providers.Binding, error) {
	r, err := s.record(setID)
	if err != nil {
		return nil, err
	}
	return decodeBinding(r, "draft_binding")
}

// SetDraftBinding 设置或清空草稿绑定。b 为 nil 即解绑。
//
// 改绑定走正常的草稿/发布流程 → 新 Revision（M1.5 spec §2.2 / §8.2）：
// 绑定决定了这个版本渲染出什么，历史版本必须能解释自己。
func (s *Service) SetDraftBinding(setID string, b *providers.Binding) error {
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	if b == nil {
		r.Set("draft_binding", nil)
	} else {
		if _, err := s.provs.Get(b.Provider); err != nil {
			return err
		}
		if !b.Models.Empty() && !b.Models.Full() {
			return fmt.Errorf(
				"configsets: 四个模型槽必须要么全空（透传）要么全满，当前 %+v", b.Models)
		}
		r.Set("draft_binding", b)
	}
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("configsets: 保存草稿绑定: %w", err)
	}
	provider := ""
	if b != nil {
		provider = b.Provider
	}
	if err := s.ev.Write(events.KindBindingChanged, "", map[string]any{
		"config_set": setID, "provider": provider,
	}); err != nil {
		s.app.Logger().Warn("写 binding.changed 事件失败", "error", err)
	}
	return nil
}

// decodeBinding 从记录的 JSON 字段解一条绑定。空字段返回 (nil, nil)。
func decodeBinding(r interface {
	UnmarshalJSONField(string, any) error
}, field string) (*providers.Binding, error) {
	var b providers.Binding
	if err := r.UnmarshalJSONField(field, &b); err != nil {
		// 空 JSON 字段解不动是正常情形，按未绑定处理。
		return nil, nil
	}
	if b.Provider == "" {
		return nil, nil
	}
	return &b, nil
}
