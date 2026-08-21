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
// 这是整个凭据机制的正确性支点（spec §6.1 / §10.1）。它保证：
// 上报给 hub 的占位符内容，被任何一台机器渲染回来都与原磁盘内容一致，
// 于是收编不会改变任何机器上的实际配置。
func TestRenderRestoreRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))

	credValues := []string{"sk-ant-abcdefghij", "ghp_klmnopqrstuv", "AKIAIOSFODNN7EXAM"}
	varValues := []string{"main", "production", "workspace-1"}

	creds := map[string]string{}
	for i, v := range credValues {
		creds[string(rune('a'+i))] = v
	}
	vars := map[string]string{}
	for i, v := range varValues {
		vars["v"+string(rune('a'+i))] = v
	}

	sec := &secrets.File{Creds: creds, Vars: vars, Machine: map[string]string{}}

	alphabet := append([]string{
		"普通文字", "\n", "{", "}", "{{", "}}", " ", `"`, ":", ",",
	}, append(credValues, varValues...)...)

	for range 500 {
		var sb strings.Builder
		for range rng.IntN(24) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		disk := sb.String()

		res := render.Restore([]byte(disk), render.Values{Creds: creds, Vars: vars})
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
	creds := map[string]string{"k": "sk-value-12345678"}
	vars := map[string]string{"tier": "1"} // 太短，会被放弃
	sec := &secrets.File{Creds: creds, Vars: vars, Machine: map[string]string{}}

	disk := `{"K":"sk-value-12345678","tier":"1"}`
	res := render.Restore([]byte(disk), render.Values{Creds: creds, Vars: vars})
	require.True(t, res.Safe)

	back, err := render.Render(res.Content, sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, disk, string(back))
}

// 往返律扩展到含 provider 值的情形（spec §11）。
func TestRoundTripWithProviderValues(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 17))

	provider := map[string]string{
		"base_url":     "https://open.bigmodel.cn/api/anthropic",
		"auth_token":   "sk-zhipu-abcdefghijklmn",
		"model":        "glm-5.1",
		"model_opus":   "glm-4.7",
		"model_sonnet": "glm-4.6-air",
		"model_haiku":  "glm-4.5-flash",
	}
	vals := render.Values{
		Creds:    map[string]string{"anthropic": "sk-ant-abcdefghij"},
		Vars:     map[string]string{"ws": "production"},
		Provider: provider,
	}
	sec := &secrets.File{
		Creds: vals.Creds, Vars: vals.Vars,
		Machine: map[string]string{}, Provider: provider,
	}

	alphabet := []string{"普通文字", "\n", "{", "}", "{{", "}}", " ", `"`, ":", ","}
	for _, v := range provider {
		alphabet = append(alphabet, v)
	}
	alphabet = append(alphabet, "sk-ant-abcdefghij", "production")

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
