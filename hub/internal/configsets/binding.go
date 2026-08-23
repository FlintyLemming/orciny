package configsets

import (
	"fmt"
	"sort"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
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

// validateBinding 是 M1.5 spec §7 的三条校验，加上 M1.6 的 endpoint_missing。
func (s *Service) validateBinding(setID string, files []protocol.FileEntry) ([]Problem, error) {
	binding, err := s.DraftBinding(setID)
	if err != nil {
		return nil, err
	}

	// 第一遍：谁引用了 provider.*、哪些端点被引用到，以及 settings.json 的内容。
	usedIn := ""
	// endpointUsedIn: 端点名 → 第一个引用它的文件路径。
	// files 已按 Path 升序（saveDraft 排过），因此「第一个」是确定的。
	endpointUsedIn := map[string]string{}
	var settings []byte
	for _, f := range files {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			continue
		}
		if f.Path == SettingsPath {
			settings = content
		}
		refs, err := protocol.Refs(content)
		if err != nil {
			continue // 语法错误由 Validate 的主循环报，这里不重复
		}
		for _, r := range refs {
			if r.Kind != protocol.RefProvider {
				continue
			}
			if usedIn == "" {
				usedIn = f.Path
			}
			if ep := protocol.EndpointOf(r.Name); ep != "" {
				if _, seen := endpointUsedIn[ep]; !seen {
					endpointUsedIn[ep] = f.Path
				}
			}
		}
	}

	var out []Problem

	// 1. 引用了但没绑：与「未定义凭据引用」同级——发布出去必然渲染失败。
	if usedIn != "" && binding == nil {
		return append(out, Problem{
			Path: usedIn, Kind: ProblemBindingMissing,
			Detail: "文件里用了 {{provider.claude.*}} 或 {{provider.openai.*}}，" +
				"但这个配置集还没有服务绑定。到「服务绑定」区选一个 AI 服务配置。",
		}), nil
	}
	if binding == nil {
		return out, nil
	}

	// 2. 绑了但没用：可能是刚绑完还没插 env 片段，不阻断。
	if usedIn == "" {
		out = append(out, Problem{
			Kind: ProblemBindingUnused, Warning: true,
			Detail: "已绑定服务配置，但没有任何文件用到 {{provider.claude.*}}。" +
				"用「插入 env 片段」把它写进 settings.json。",
		})
		return out, nil
	}

	prov, err := s.provs.Get(binding.Provider)
	if err != nil {
		return nil, err
	}

	// 3. auth_field 键名不符（M1.5 spec §3.3）。占位符替的是值，替不了键名。
	// 只比 claude 端点：openai 侧的 auth_field 是自由文本（M1.6 spec §4.2）。
	want := providers.ClaudeOf(prov).AuthField
	if got := authFieldMismatch(settings, want); got != "" {
		out = append(out, Problem{
			Path: SettingsPath, Kind: ProblemAuthFieldMismatch,
			Detail: fmt.Sprintf(
				"绑定的服务配置用 %s 鉴权，但 settings.json 里写的是 %s。"+
					"占位符只能替换值，替不了键名——需要把这一行的键名改掉。", want, got),
			Fix: &Fix{Kind: FixReplaceEnvKey, From: got, To: want},
		})
	}

	// 4. 引用的端点没配（M1.6 spec §4.3）。阻断级：发布出去必然渲染失败。
	//
	// spec §3.2 说的「显式前缀下这是一行判断」就是这里——端点段直接写在
	// 占位符里，hub 不需要任何「这个路径属于哪个工具」的推断。
	for _, ep := range []string{providers.EndpointClaude, providers.EndpointOpenAI} {
		path, used := endpointUsedIn[ep]
		if !used {
			continue
		}
		configured := false
		label := ""
		switch ep {
		case providers.EndpointClaude:
			configured, label = providers.ClaudeOf(prov).Configured(), "Claude"
		case providers.EndpointOpenAI:
			configured, label = providers.OpenAIOf(prov).Configured(), "OpenAI"
		}
		if configured {
			continue
		}
		out = append(out, Problem{
			Path: path, Kind: ProblemEndpointMissing,
			Detail: fmt.Sprintf(
				"文件 %s 引用了 {{provider.%s.*}}，但绑定的服务配置「%s」没有配置 %s 端点。"+
					"到「AI 服务」页给它补上 base_url，或改引用另一侧端点。",
				path, ep, prov.GetString("name"), label),
		})
	}
	return out, nil
}

// authFieldMismatch 返回 settings.json 的 env 里实际承载
// {{provider.claude.auth_token}} 的键名；与 want 一致或找不到时返回空串。
//
// 只看值是那个占位符的键：少数平台会同时设两个键（spec §2.3 的统计里
// 有重叠），把用户自己写的另一个键当成错误会很吵。
func authFieldMismatch(settings []byte, want string) string {
	if len(settings) == 0 || !gjson.ValidBytes(settings) {
		return ""
	}
	env := gjson.GetBytes(settings, "env")
	if !env.Exists() {
		return ""
	}
	var others []string
	matched := false
	env.ForEach(func(k, v gjson.Result) bool {
		if v.String() != tokenAuthToken {
			return true
		}
		if k.String() == want {
			matched = true
			return false
		}
		others = append(others, k.String())
		return true
	})
	if matched || len(others) == 0 {
		return ""
	}
	sort.Strings(others) // 多个候选时取值确定，测试才可复现
	return others[0]
}

// FixAuthField 把 settings.json 的 env 里承载 {{provider.claude.auth_token}} 的
// 键名改成绑定 Provider 的 claude 端点 auth_field（M1.5 spec §7 第 3 条）。
//
// 改动落在**草稿**上：用户在 diff 里看得见、发布前可撤销。不自动改写——
// 那会让用户的文件在背后被动过，违背 M1 立下的「宁可不动，不可写坏」。
func (s *Service) FixAuthField(setID string) error {
	binding, err := s.DraftBinding(setID)
	if err != nil {
		return err
	}
	if binding == nil {
		return fmt.Errorf("configsets: 没有服务绑定，无从判断该用哪个键名")
	}
	prov, err := s.provs.Get(binding.Provider)
	if err != nil {
		return err
	}
	want := providers.ClaudeOf(prov).AuthField

	files, err := s.Draft(setID)
	if err != nil {
		return err
	}
	var entry *protocol.FileEntry
	for i := range files {
		if files[i].Path == SettingsPath {
			entry = &files[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("configsets: 草稿里没有 %s", SettingsPath)
	}
	content, err := s.blobs.Get(entry.Hash)
	if err != nil {
		return err
	}
	got := authFieldMismatch(content, want)
	if got == "" {
		return nil // 已经是对的，幂等
	}

	out, err := sjson.DeleteBytes(content, "env."+got)
	if err != nil {
		return fmt.Errorf("configsets: 删除 env.%s: %w", got, err)
	}
	out, err = sjson.SetBytes(out, "env."+want, tokenAuthToken)
	if err != nil {
		return fmt.Errorf("configsets: 写入 env.%s: %w", want, err)
	}
	_, err = s.SetDraftFile(setID, SettingsPath, out, entry.Mode, entry.Keys)
	return err
}
