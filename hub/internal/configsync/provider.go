package configsync

import (
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

var (
	// ErrBindingMissing：Revision 引用了 {{provider.*}} 但没有绑定。
	// 这种状态本应被发布校验挡住（spec §7），此处是纵深防御（spec §5.1）。
	ErrBindingMissing = errors.New("configsync: 引用了 {{provider.*}} 但没有服务绑定")

	// ErrEmptyModelSlot：引用了某个模型槽，但绑定里该槽是空的（透传模式）。
	// 发一个空串下去会让 Claude Code 去打一个空模型名，错误现场离原因很远。
	ErrEmptyModelSlot = errors.New("configsync: 引用了模型槽但绑定里该槽为空")

	// ErrEndpointMissing：引用了某个端点，但绑定的 provider 没配它。
	// 与 ErrBindingMissing 同一档的纵深防御（M1.6 spec §4.3）。
	ErrEndpointMissing = errors.New("configsync: 引用了某端点但绑定的服务配置没配它")
)

// providerValues 组装快照里的 provider 九个键（M1.6 spec §3.4）。
//
// 裁剪口径不变：只发 refs.provider_keys 里出现过的键。这是最小权限，
// 也是一条实际的防线——少一个键少一处泄露面，两个端点的 key 尤其。
// 判定全程**不读 blob 内容**，这正是 M1 立 refs 字段的初衷。
func (s *Service) providerValues(
	machineID string, head *core.Record, refs configsets.Refs,
) (map[string]string, error) {
	var b providers.Binding
	// 空 JSON 字段解不动是正常情形，按未绑定处理。
	_ = head.UnmarshalJSONField("binding", &b)

	if b.Provider == "" {
		if len(refs.ProviderKeys) > 0 {
			return nil, fmt.Errorf("%w：版本 %s 引用了 %v",
				ErrBindingMissing, head.Id, refs.ProviderKeys)
		}
		return nil, nil
	}
	if len(refs.ProviderKeys) == 0 {
		return nil, nil // 绑了但没用，警告级（spec §7 第 2 条），照常下发
	}
	if s.d.Providers == nil {
		return nil, fmt.Errorf("configsync: providers 服务未装配")
	}

	// 门槛放在这里：确认真要下发 provider 值之后才检查，
	// 没绑定或没引用的机器不受影响（spec §10）。
	if err := s.checkAgentVersion(machineID); err != nil {
		return nil, err
	}

	prov, err := s.d.Providers.Get(b.Provider)
	if err != nil {
		return nil, err
	}

	// 先把「引用到的端点都配了吗」判掉（spec §4.3 的纵深防御）。
	// 这一步必须在解密之前：没配的端点连 key 都不该去解。
	claude := providers.ClaudeOf(prov)
	openai := providers.OpenAIOf(prov)
	for _, k := range refs.ProviderKeys {
		ep := protocol.EndpointOf(k)
		configured := false
		switch ep {
		case providers.EndpointClaude:
			configured = claude.Configured()
		case providers.EndpointOpenAI:
			configured = openai.Configured()
		default:
			continue // 非内置名，protocol 的词法早就拦过
		}
		if !configured {
			return nil, fmt.Errorf("%w：版本 %s 引用了 {{provider.%s}}，"+
				"但服务配置 %q 没有配置 %s 端点",
				ErrEndpointMissing, head.Id, k, prov.GetString("name"), ep)
		}
	}

	all := map[string]string{
		"claude.base_url":     claude.BaseURL,
		"claude.model":        b.Models.Main,
		"claude.model_opus":   b.Models.Opus,
		"claude.model_sonnet": b.Models.Sonnet,
		"claude.model_haiku":  b.Models.Haiku,
		"openai.base_url":     openai.BaseURL,
		// openai.model 取 provider 的 default_model，不是 binding：
		// binding 本期恒指 claude 端点（spec §1.3 / §3.4）。这是一处刻意的
		// 不对称，接 Codex 时会连同「配置集怎么绑 openai 端点」重新设计。
		"openai.model": openai.DefaultModel,
	}
	// 两把 key 按需解密：没被引用就不解，也就不会有明文进内存。
	for _, pair := range []struct{ key, endpoint string }{
		{"claude.auth_token", providers.EndpointClaude},
		{"openai.api_key", providers.EndpointOpenAI},
	} {
		if !contains(refs.ProviderKeys, pair.key) {
			continue
		}
		v, err := s.d.Providers.Key(prov, pair.endpoint)
		if err != nil {
			return nil, fmt.Errorf("configsync: 取服务配置 %s 的 %s 端点 key: %w",
				b.Provider, pair.endpoint, err)
		}
		all[pair.key] = v
	}

	out := make(map[string]string, len(refs.ProviderKeys))
	for _, k := range refs.ProviderKeys {
		v, ok := all[k]
		if !ok {
			continue // 非内置名，protocol 的词法早就拦过，这里只是稳一手
		}
		if v == "" {
			return nil, fmt.Errorf("%w：{{provider.%s}} 被引用，但绑定里该值是空的",
				ErrEmptyModelSlot, k)
		}
		out[k] = v
	}
	return out, nil
}

// ErrAgentTooOld：目标机器的 agent 太老，渲染不了 {{provider.*}}。
var ErrAgentTooOld = errors.New("configsync: agent 版本过低")

// checkAgentVersion 是 spec §10 的定向门槛。
//
// 不下发 好过 发下去让它渲染失败再回滚：失败回滚是兜底不是主路径，
// 而且回滚的错误信息（「占位符语法错误」）离真实原因（「agent 老了」）很远。
func (s *Service) checkAgentVersion(machineID string) error {
	m, err := s.d.App.FindRecordById("machines", machineID)
	if err != nil {
		return fmt.Errorf("configsync: 机器 %s 不存在: %w", machineID, err)
	}
	raw := strings.TrimPrefix(m.GetString("agent_version"), "v")
	v, err := semver.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w（版本号 %q 解析不出来），请升级到 v%s 或更高后重试",
			ErrAgentTooOld, m.GetString("agent_version"), orciny.MinProviderAgentVersion)
	}
	// 比大小前丢掉 pre-release 与 build 元数据。
	//
	// goreleaser / Makefile 注入的是 git describe 的结果，形如
	// v0.2.0-38-g3161fd4 ——它的语义是「v0.2.0 之后第 38 个提交」，功能上
	// 只会比 v0.2.0 更新；但按 semver 的规矩带 pre-release 的版本**小于**
	// 同号正式版，直接比会把每一个非 tag 构建都判成过老。
	v.Pre, v.Build = nil, nil
	if v.LT(orciny.MinProviderAgentVersion) {
		return fmt.Errorf("%w（v%s < v%s），请升级 agent 后重试",
			ErrAgentTooOld, v, orciny.MinProviderAgentVersion)
	}
	return nil
}

// isBindingFault 判定一个快照组装失败是否属于「绑定相关、需要归因」的那一类。
//
// ErrEndpointMissing 属于这一类：指派置 failed 并写明原因，
// **不置 degraded**——机器上什么都没被改过（M1.6 spec §4.3）。
func isBindingFault(err error) bool {
	return errors.Is(err, ErrAgentTooOld) ||
		errors.Is(err, ErrBindingMissing) ||
		errors.Is(err, ErrEmptyModelSlot) ||
		errors.Is(err, ErrEndpointMissing)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// failAssignment 把指派置 failed 并写明原因。
//
// 不置 degraded：机器上什么都没被改过，不需要人工解除——
// 升级 agent 或修好绑定之后，下一次 apply 成功就会把状态翻回 aligned。
func (s *Service) failAssignment(machineID string, cause error) {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		return
	}
	assign.Set("state", configsets.StateFailed)
	assign.Set("last_error", cause.Error())
	if err := s.d.App.Save(assign); err != nil {
		s.log.Warn("保存指派状态失败", "machine", machineID, "error", err)
		return
	}
	s.log.Warn("拒绝下发含服务绑定的快照", "machine", machineID, "error", cause)
	if err := s.d.Events.Write(events.KindApplyFailed, machineID, map[string]any{
		"error": cause.Error(), "reason": "binding",
	}); err != nil {
		s.log.Warn("写 apply.failed 事件失败", "error", err)
	}
}
