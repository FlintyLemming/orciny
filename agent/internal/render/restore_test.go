package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestRestoreReplacesSecretKey(t *testing.T) {
	got := render.Restore(
		[]byte(`{"env":{"K":"sk-ant-realvalue"}}`),
		render.Values{Provider: map[string]string{"claude.auth_token": "sk-ant-realvalue"}},
	)
	require.True(t, got.Safe)
	require.False(t, got.Partial)
	require.Equal(t, `{"env":{"K":"{{provider.claude.auth_token}}"}}`, string(got.Content))
}

// 按值长度降序替换：短值是长值的子串时不能先替短的（spec §6.4）。
func TestRestoreReplacesLongestFirst(t *testing.T) {
	got := render.Restore(
		[]byte(`{"long":"sk-abcdefgh-suffix","short":"sk-abcdefgh"}`),
		render.Values{Provider: map[string]string{
			"openai.api_key":    "sk-abcdefgh",
			"claude.auth_token": "sk-abcdefgh-suffix",
		}},
	)
	require.True(t, got.Safe)
	require.Equal(t,
		`{"long":"{{provider.claude.auth_token}}","short":"{{provider.openai.api_key}}"}`,
		string(got.Content))
}

// 一个值出现多次：全部替掉，漏一处等于没脱敏。
func TestRestoreReplacesEveryOccurrence(t *testing.T) {
	got := render.Restore(
		[]byte("key=sk-value-1234\n再说一遍 sk-value-1234\n"),
		render.Values{Provider: map[string]string{"claude.auth_token": "sk-value-1234"}},
	)
	require.True(t, got.Safe)
	require.Equal(t, 2,
		strings.Count(string(got.Content), "{{provider.claude.auth_token}}"))
	require.NotContains(t, string(got.Content), "sk-value-1234")
}

// 磁盘上字面的 {{ 必须转义，否则往返律破掉。
func TestRestoreEscapesLiteralBraces(t *testing.T) {
	got := render.Restore([]byte("模板写法是 {{var.x}} 这样"), render.Values{})
	require.True(t, got.Safe)
	require.Equal(t, "模板写法是 {{{{var.x}} 这样", string(got.Content))

	// 往返：restore 的结果再 render 回去必须一字不差
	sec := &secrets.File{}
	back, err := render.Render(got.Content, sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, "模板写法是 {{var.x}} 这样", string(back))
}

// 变量：长度 ≥ 4 才替。
func TestRestoreSkipsShortVariables(t *testing.T) {
	got := render.Restore(
		[]byte(`{"branch":"main","tier":"1"}`),
		render.Values{Vars: map[string]string{"branch": "main", "tier": "1"}},
	)
	require.Contains(t, string(got.Content), "{{var.branch}}")
	require.Contains(t, string(got.Content), `"1"`, "太短的变量值不替")
	require.True(t, got.Partial, "放弃还原某个变量时必须标记")
	require.True(t, got.Safe, "变量还原失败不影响 Safe——它不是秘密")
}

// 变量值在内容里出现次数与基线对不上：仍然替，但标记 Partial 让人复核。
func TestRestoreMarksPartialOnVariableCountMismatch(t *testing.T) {
	base := []byte(`{"a":"{{var.ws}}"}`)                    // 渲染时出现 1 次
	cur := []byte(`{"a":"main","b":"main-ish","c":"main"}`) // 现在出现 3 次（含 main-ish 中的 main）
	got := render.RestoreWithBase(cur, base, render.Values{Vars: map[string]string{"ws": "main"}})
	require.True(t, got.Partial)
	require.True(t, got.Safe)
}

func TestRestoreNoPartialWhenCountMatches(t *testing.T) {
	base := []byte(`{"a":"{{var.ws}}"}`)
	cur := []byte(`{"a":"main"}`)
	got := render.RestoreWithBase(cur, base, render.Values{Vars: map[string]string{"ws": "main"}})
	require.False(t, got.Partial)
	require.Equal(t, `{"a":"{{var.ws}}"}`, string(got.Content))
}

// 内容里根本没出现的秘密/变量不影响任何标记。
func TestRestoreIgnoresAbsentValues(t *testing.T) {
	got := render.Restore(
		[]byte("普通内容"),
		render.Values{
			Provider: map[string]string{"claude.auth_token": "sk-not-here-1234"},
			Vars:     map[string]string{"ws": "nowhere"},
		},
	)
	require.True(t, got.Safe)
	require.False(t, got.Partial)
	require.Equal(t, "普通内容", string(got.Content))
}

func TestRestoreOfEmptyContent(t *testing.T) {
	got := render.Restore(nil,
		render.Values{Provider: map[string]string{"claude.auth_token": "sk-value-1234"}})
	require.True(t, got.Safe)
	require.Empty(t, got.Content)
}

func TestRestoreAuthTokenGoesToSecretTier(t *testing.T) {
	v := render.Values{
		Provider: map[string]string{"claude.auth_token": "sk-zhipu-abcdefghij"},
	}
	res := render.Restore([]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-zhipu-abcdefghij"}}`), v)
	require.True(t, res.Safe)
	require.Equal(t, `{"env":{"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"}}`,
		string(res.Content))
	require.NotContains(t, string(res.Content), "sk-zhipu")
}

// auth_token 是秘密：替不回去就必须判定不安全，调用方只报路径（spec §4.2）。
func TestRestoreUnsafeWhenAuthTokenSurvives(t *testing.T) {
	v := render.Values{
		Provider: map[string]string{"claude.auth_token": "sk-zhipu-abcdefghij"},
	}
	// 值被人为切碎成两段，替换替不干净，最后一道闸要拦住它。
	disk := `{"a":"sk-zhipu-abcdefghij","b":"sk-zhipu-abcdefghij{{"}`
	res := render.Restore([]byte(disk), v)
	if res.Safe {
		require.NotContains(t, string(res.Content), "sk-zhipu-abcdefghij")
	}
}

func TestRestoreBaseURLGoesToVarTier(t *testing.T) {
	v := render.Values{
		Provider: map[string]string{
			"claude.base_url": "https://open.bigmodel.cn/api/anthropic",
		},
	}
	res := render.Restore(
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic"}}`), v)
	require.True(t, res.Safe)
	require.Contains(t, string(res.Content), "{{provider.claude.base_url}}")
}

// 短模型 id 触发 RestorePartial 让人复核——这正是 MinVarLen 想要的行为。
func TestRestoreShortModelIDIsPartial(t *testing.T) {
	v := render.Values{Provider: map[string]string{"claude.model": "k2"}}
	res := render.Restore([]byte(`{"env":{"ANTHROPIC_MODEL":"k2"}}`), v)
	require.True(t, res.Partial)
	require.Contains(t, string(res.Content), `"k2"`, "太短，放弃还原但不阻断")
}

// 四槽同值：按值去重，只留一个 token；计数对不上因此标记 Partial。
func TestRestoreDedupesIdenticalModelSlots(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.model": "glm-5.1", "claude.model_opus": "glm-5.1",
		"claude.model_sonnet": "glm-5.1", "claude.model_haiku": "glm-5.1",
	}}
	disk := `{"env":{"ANTHROPIC_MODEL":"glm-5.1","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.1"}}`
	res := render.Restore([]byte(disk), v)
	require.True(t, res.Safe)
	require.Equal(t, 2, strings.Count(string(res.Content), "{{provider.claude.model}}"))
	require.NotContains(t, string(res.Content), "{{provider.claude.model_opus}}")
	require.True(t, res.Partial, "出现次数与基线对不上，必须让人复核")
}

// 两个端点的 key **都**是秘密档：必须还原，还原不了就 Safe=false。
// 一把 API key 落进尽力档就等于有机会被明文上传（M1.6 spec §3.4）。
func TestBothEndpointKeysAreSecrets(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.auth_token": "sk-claude-abcdef123456",
		"openai.api_key":    "sk-openai-zyxwvu654321",
	}}
	content := []byte("claude=sk-claude-abcdef123456 openai=sk-openai-zyxwvu654321")

	res := render.Restore(content, v)
	require.True(t, res.Safe)
	require.Equal(t,
		"claude={{provider.claude.auth_token}} openai={{provider.openai.api_key}}",
		string(res.Content))
}

// 秘密档的最后一道闸：任一已知秘密值仍能被搜到即判定还原失败。
func TestUnrestorableOpenAIKeyMakesUnsafe(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		// 值恰好落在 "{" 后面，替换后往返律破掉 → Safe=false
		"openai.api_key": "sk-openai-zyxwvu654321",
	}}
	res := render.Restore([]byte("{sk-openai-zyxwvu654321"), v)
	require.False(t, res.Safe, "还原不干净时调用方不得上报内容")
}

// 其余七个键进尽力档：受 MinVarLen 约束、替不掉也放行。
func TestSevenNonSecretProviderKeysAreBestEffort(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.base_url":     "https://open.bigmodel.cn/api/anthropic",
		"claude.model":        "glm-5.2",
		"claude.model_opus":   "glm-5.2",
		"claude.model_sonnet": "glm-5.2",
		"claude.model_haiku":  "glm-4.7",
		"openai.base_url":     "https://open.bigmodel.cn/api/paas/v4",
		"openai.model":        "glm-4.7",
	}}
	res := render.Restore([]byte("url=https://open.bigmodel.cn/api/anthropic"), v)
	require.True(t, res.Safe, "尽力档替不掉也不影响 Safe")
	require.Contains(t, string(res.Content), "{{provider.claude.base_url}}")
}

// 同值去重按 ProviderKeys 的下标定序，留下靠前的那个（M1.5 的 rankOf 语义不变，
// 只是名字变长了）。四槽同值时留下的是 claude.model。
func TestSameValueKeepsEarlierProviderKey(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.model":        "glm-5.2",
		"claude.model_opus":   "glm-5.2",
		"claude.model_sonnet": "glm-5.2",
		"claude.model_haiku":  "glm-5.2",
	}}
	res := render.Restore([]byte("model=glm-5.2"), v)
	require.Equal(t, "model={{provider.claude.model}}", string(res.Content))
}

// 两个端点用同一把 key 时，留下的是 ProviderKeys 里靠前的那个
// ——claude.auth_token 排在 openai.api_key 前面（spec §7）。
func TestSameKeyOnBothEndpointsKeepsClaude(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.auth_token": "sk-shared-abcdef123456",
		"openai.api_key":    "sk-shared-abcdef123456",
	}}
	res := render.Restore([]byte("k=sk-shared-abcdef123456"), v)
	require.True(t, res.Safe)
	require.Equal(t, "k={{provider.claude.auth_token}}", string(res.Content))
}

// 往返律：restore 的产物必须能被 render 回原内容（M1 spec §6.1）。
func TestRoundTripWithBothEndpoints(t *testing.T) {
	v := render.Values{
		Vars: map[string]string{"workspace": "orciny-main"},
		Provider: map[string]string{
			"claude.auth_token": "sk-claude-abcdef123456",
			"openai.api_key":    "sk-openai-zyxwvu654321",
			"claude.base_url":   "https://open.bigmodel.cn/api/anthropic",
		},
	}
	original := []byte("ws=orciny-main claude=sk-claude-abcdef123456 " +
		"openai=sk-openai-zyxwvu654321 url=https://open.bigmodel.cn/api/anthropic")

	res := render.Restore(original, v)
	require.True(t, res.Safe)

	back, err := render.Render(res.Content, func(r protocol.Ref) (string, bool) {
		switch r.Kind {
		case protocol.RefVar:
			s, ok := v.Vars[r.Name]
			return s, ok
		case protocol.RefProvider:
			s, ok := v.Provider[r.Name]
			return s, ok
		}
		return "", false
	})
	require.NoError(t, err)
	require.Equal(t, original, back)
}
