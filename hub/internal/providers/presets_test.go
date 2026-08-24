package providers_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// 种子数据自检：auth_field 取值合法、四槽要么全空要么全满（spec §11）。
func TestPresetSeedIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range providers.Presets() {
		require.NotEmpty(t, p.ID, "预设必须有 id")
		require.False(t, seen[p.ID], "预设 id %s 重复", p.ID)
		seen[p.ID] = true

		require.NotEmpty(t, p.Name, "%s 缺显示名", p.ID)
		require.NotEmpty(t, p.Claude.BaseURL, "%s 缺 claude base_url", p.ID)
		require.Contains(t,
			[]string{providers.AuthToken, providers.AuthAPIKey}, p.Claude.AuthField,
			"%s 的 claude auth_field 非法", p.ID)

		// 四槽全空 = 透传（中转官方 Claude）；全满 = 有自家模型。
		// 半填是配置错误：Claude Code 会拿 claude-haiku-* 去打人家的 endpoint。
		require.True(t, p.Claude.Defaults.Empty() || p.Claude.Defaults.Full(),
			"%s 的四个模型槽必须要么全空要么全满，当前 %+v", p.ID, p.Claude.Defaults)

		// [1m] 是槽位上的上下文声明，不是一个独立模型：它只出现在 Defaults
		// 里，模型清单只列基名，否则下拉里同一个模型会并排出现两次。
		claudeModelMap := map[string]bool{}
		for _, m := range p.Claude.Models {
			require.False(t, providers.HasOneM(m.Name),
				"%s 的模型清单不该出现 1M 变体 %q，标记只写进 Defaults", p.ID, m.Name)
			claudeModelMap[m.Name] = m.OneM
		}

		if p.Claude.Defaults.Full() {
			base := providers.StripOneM(p.Claude.Defaults.Main)
			require.Contains(t, claudeModelMap, base,
				"%s 的默认主模型基名必须在模型清单里", p.ID)
			for _, slot := range []string{
				p.Claude.Defaults.Main, p.Claude.Defaults.Opus,
				p.Claude.Defaults.Sonnet, p.Claude.Defaults.Haiku,
			} {
				if providers.HasOneM(slot) {
					slotBase := providers.StripOneM(slot)
					require.True(t, claudeModelMap[slotBase],
						"%s 的 Defaults 声明了 %s[1m]，但 Claude.Models 里的 %s 未标记 OneM: true",
						p.ID, slotBase, slotBase)
				}
			}
		}
		// 订阅域预留（spec §9）：填了 type 就必须填 mode，反之亦然。
		require.Equal(t, p.CollectorType == "", p.CollectorMode == "",
			"%s 的 CollectorType 与 CollectorMode 必须同时为空或同时非空", p.ID)

	}
	require.NotEmpty(t, providers.Presets(), "种子表不能是空的")
}

func TestPresetByID(t *testing.T) {
	p, ok := providers.PresetByID("zhipu")
	require.True(t, ok)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", p.Claude.BaseURL)

	_, ok = providers.PresetByID("不存在的平台")
	require.False(t, ok)
}

func TestModelSlotsEmptyAndFull(t *testing.T) {
	require.True(t, providers.ModelSlots{}.Empty())
	require.False(t, providers.ModelSlots{}.Full())

	full := providers.ModelSlots{Main: "a", Opus: "a", Sonnet: "a", Haiku: "a"}
	require.True(t, full.Full())
	require.False(t, full.Empty())

	half := providers.ModelSlots{Main: "a"}
	require.False(t, half.Empty())
	require.False(t, half.Full())
}

// Presets 返回的切片被改了不能影响下一次调用——它是编译期常量的门面。
func TestPresetsIsNotAliased(t *testing.T) {
	a := providers.Presets()
	a[0].Claude.BaseURL = "https://tampered.test"
	b := providers.Presets()
	require.NotEqual(t, "https://tampered.test", b[0].Claude.BaseURL)
}

func TestEveryPresetHasAClaudeEndpoint(t *testing.T) {
	for _, p := range providers.Presets() {
		require.NotEmpty(t, p.Claude.BaseURL, "%s 必须有 claude 端点", p.ID)
		require.Contains(t,
			[]string{providers.AuthToken, providers.AuthAPIKey}, p.Claude.AuthField,
			"%s 的 claude auth_field 只能二选一", p.ID)
		require.NotEmpty(t, p.Claude.Models, "%s 的 claude 端点要有模型清单", p.ID)

		// 四槽要么全空（透传）要么全满，与 Store 的约束同源。
		d := p.Claude.Defaults
		require.True(t, d.Empty() || d.Full(), "%s 的四槽半填了", p.ID)
	}
}

// openai 端点可以为空（该平台没有这个口），但一旦填了就要填齐。
func TestPresetOpenAIEndpointIsCompleteWhenPresent(t *testing.T) {
	for _, p := range providers.Presets() {
		if p.OpenAI.BaseURL == "" {
			require.Empty(t, p.OpenAI.Models,
				"%s 没有 openai base_url 却填了模型清单", p.ID)
			continue
		}
		require.Equal(t, providers.DefaultOpenAIAuthField, p.OpenAI.AuthField,
			"%s 的 openai auth_field 默认就该是 OPENAI_API_KEY", p.ID)
		require.NotEmpty(t, p.OpenAI.Models, "%s 的 openai 端点要有模型清单", p.ID)
		require.Contains(t, p.OpenAI.Models, p.OpenAI.DefaultModel,
			"%s 的 openai 默认模型必须在清单里", p.ID)
	}
}

// 火山方舟同一个域名下两个口，用错会走计费不同的通道（spec §5.6）。
func TestVolcengineHasBothEndpointsOnDifferentPaths(t *testing.T) {
	p, ok := providers.PresetByID("volcengine")
	require.True(t, ok)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/coding", p.Claude.BaseURL,
		"/api/coding 才是 Anthropic 协议口")
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3", p.OpenAI.BaseURL,
		"/api/v3 是 OpenAI 协议口")
}

// Anthropic 官方没有 OpenAI 协议口。UI 会显示「该平台未提供 OpenAI 端点」。
func TestAnthropicHasNoOpenAIEndpoint(t *testing.T) {
	p, ok := providers.PresetByID("anthropic")
	require.True(t, ok)
	require.Empty(t, p.OpenAI.BaseURL)
}

func TestPresetsReturnsACopy(t *testing.T) {
	a := providers.Presets()
	a[0].Name = "被改过了"
	b := providers.Presets()
	require.NotEqual(t, "被改过了", b[0].Name)
}

// 1M 声明是模型名上的语法，识别规则与 Claude Code 的 /\[1m\]/i 对齐。
func TestOneMMarker(t *testing.T) {
	cases := []struct {
		model string
		has   bool
		base  string
	}{
		{"glm-5.2[1m]", true, "glm-5.2"},
		{"glm-5.2[1M]", true, "glm-5.2"},    // Claude Code 大小写不敏感
		{"glm-5.2 [1m]  ", true, "glm-5.2"}, // 尾随空格不该影响判断
		{"glm-5.2", false, "glm-5.2"},
		{"glm-5.2[1m]-turbo", false, "glm-5.2[1m]-turbo"}, // 只认结尾
		{"", false, ""},
	}
	for _, c := range cases {
		require.Equal(t, c.has, providers.HasOneM(c.model), "HasOneM(%q)", c.model)
		require.Equal(t, c.base, providers.StripOneM(c.model), "StripOneM(%q)", c.model)
	}
}
