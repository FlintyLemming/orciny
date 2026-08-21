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
		require.NotEmpty(t, p.BaseURL, "%s 缺 base_url", p.ID)
		require.Contains(t,
			[]string{providers.AuthToken, providers.AuthAPIKey}, p.AuthField,
			"%s 的 auth_field 非法", p.ID)

		// 四槽全空 = 透传（中转官方 Claude）；全满 = 有自家模型。
		// 半填是配置错误：Claude Code 会拿 claude-haiku-* 去打人家的 endpoint。
		require.True(t, p.Defaults.Empty() || p.Defaults.Full(),
			"%s 的四个模型槽必须要么全空要么全满，当前 %+v", p.ID, p.Defaults)

		if p.Defaults.Full() {
			require.Contains(t, p.Models, p.Defaults.Main,
				"%s 的默认主模型必须在模型清单里", p.ID)
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
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", p.BaseURL)

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
	a[0].BaseURL = "https://tampered.test"
	b := providers.Presets()
	require.NotEqual(t, "https://tampered.test", b[0].BaseURL)
}
