package render_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
)

// render(restore(x)) == x：对任意磁盘内容与任意值集合成立。
//
// 这是整个秘密注入机制的正确性支点（spec §6.1 / §10.1）。它保证：
// 上报给 hub 的占位符内容，被任何一台机器渲染回来都与原磁盘内容一致，
// 于是收编不会改变任何机器上的实际配置。
func TestRenderRestoreRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))

	secretValues := []string{"sk-ant-abcdefghij", "ghp_klmnopqrstuv"}
	varValues := []string{"main", "production", "workspace-1"}

	// 两个端点的 key 都是秘密档，正好各占一个。
	provider := map[string]string{
		"claude.auth_token": secretValues[0],
		"openai.api_key":    secretValues[1],
	}
	vars := map[string]string{}
	for i, v := range varValues {
		vars["v"+string(rune('a'+i))] = v
	}

	sec := &secrets.File{Vars: vars, Machine: map[string]string{}, Provider: provider}

	alphabet := append([]string{
		"普通文字", "\n", "{", "}", "{{", "}}", " ", `"`, ":", ",",
	}, append(secretValues, varValues...)...)

	for range 500 {
		var sb strings.Builder
		for range rng.IntN(24) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		disk := sb.String()

		res := render.Restore([]byte(disk), render.Values{Vars: vars, Provider: provider})
		if !res.Safe {
			continue // 判定为不安全的内容不会被上报，往返律对它不适用
		}
		back, err := render.Render(res.Content, sec.Lookup)
		require.NoError(t, err, "输入 %q 还原后无法重新渲染", disk)
		require.Equal(t, disk, string(back), "往返不一致：%q", disk)
	}
}

// 变量被放弃还原时往返律仍要成立——只是内容里留着字面值而已。
func TestRoundTripHoldsWithPartialRestore(t *testing.T) {
	provider := map[string]string{"claude.auth_token": "sk-value-12345678"}
	vars := map[string]string{"tier": "1"} // 太短，会被放弃
	sec := &secrets.File{Vars: vars, Machine: map[string]string{}, Provider: provider}

	disk := `{"K":"sk-value-12345678","tier":"1"}`
	res := render.Restore([]byte(disk), render.Values{Vars: vars, Provider: provider})
	require.True(t, res.Safe)

	back, err := render.Render(res.Content, sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, disk, string(back))
}

// 往返律扩展到含 provider 值的情形（spec §11）。
func TestRoundTripWithProviderValues(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))

	provider := map[string]string{
		"claude.base_url":     "https://open.bigmodel.cn/api/anthropic",
		"claude.auth_token":   "sk-zhipu-abcdefghijklmn",
		"claude.model":        "glm-5.1",
		"claude.model_opus":   "glm-4.7",
		"claude.model_sonnet": "glm-4.6-air",
		"claude.model_haiku":  "glm-4.5-flash",
		"openai.base_url":     "https://open.bigmodel.cn/api/paas/v4",
		"openai.api_key":      "sk-openai-zyxwvutsrq",
		"openai.model":        "glm-4.7-openai",
	}
	vals := render.Values{
		Vars:     map[string]string{"ws": "production"},
		Provider: provider,
	}
	sec := &secrets.File{
		Vars: vals.Vars, Machine: map[string]string{}, Provider: provider,
	}

	alphabet := []string{"普通文字", "\n", "{", "}", "{{", "}}", " ", `"`, ":", ","}
	for _, v := range provider {
		alphabet = append(alphabet, v)
	}
	alphabet = append(alphabet, "production")

	for range 500 {
		var sb strings.Builder
		for range rng.IntN(24) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		disk := sb.String()

		res := render.Restore([]byte(disk), vals)
		if !res.Safe {
			continue // 判定为不安全的内容不会被上报，往返律对它不适用
		}
		back, err := render.Render(res.Content, sec.Lookup)
		require.NoError(t, err, "输入 %q 还原后无法重新渲染", disk)
		require.Equal(t, disk, string(back), "往返不一致：%q", disk)
	}
}
