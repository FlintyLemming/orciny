package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
)

func TestRestoreReplacesCredential(t *testing.T) {
	got := render.Restore(
		[]byte(`{"env":{"K":"sk-ant-realvalue"}}`),
		map[string]string{"anthropic_key": "sk-ant-realvalue"},
		nil,
	)
	require.True(t, got.Safe)
	require.False(t, got.Partial)
	require.Equal(t, `{"env":{"K":"{{cred.anthropic_key}}"}}`, string(got.Content))
}

// 按值长度降序替换：短值是长值的子串时不能先替短的（spec §6.4）。
func TestRestoreReplacesLongestFirst(t *testing.T) {
	got := render.Restore(
		[]byte(`{"long":"sk-abcdefgh-suffix","short":"sk-abcdefgh"}`),
		map[string]string{
			"short_key": "sk-abcdefgh",
			"long_key":  "sk-abcdefgh-suffix",
		},
		nil,
	)
	require.True(t, got.Safe)
	require.Equal(t,
		`{"long":"{{cred.long_key}}","short":"{{cred.short_key}}"}`,
		string(got.Content))
}

// 一个值出现多次：全部替掉，漏一处等于没脱敏。
func TestRestoreReplacesEveryOccurrence(t *testing.T) {
	got := render.Restore(
		[]byte("key=sk-value-1234\n再说一遍 sk-value-1234\n"),
		map[string]string{"k": "sk-value-1234"},
		nil,
	)
	require.True(t, got.Safe)
	require.Equal(t, 2, strings.Count(string(got.Content), "{{cred.k}}"))
	require.NotContains(t, string(got.Content), "sk-value-1234")
}

// 磁盘上字面的 {{ 必须转义，否则往返律破掉。
func TestRestoreEscapesLiteralBraces(t *testing.T) {
	got := render.Restore([]byte("模板写法是 {{cred.x}} 这样"), nil, nil)
	require.True(t, got.Safe)
	require.Equal(t, "模板写法是 {{{{cred.x}} 这样", string(got.Content))

	// 往返：restore 的结果再 render 回去必须一字不差
	sec := &secrets.File{}
	back, err := render.Render(got.Content, sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, "模板写法是 {{cred.x}} 这样", string(back))
}

// 变量：长度 ≥ 4 才替。
func TestRestoreSkipsShortVariables(t *testing.T) {
	got := render.Restore(
		[]byte(`{"branch":"main","tier":"1"}`),
		nil,
		map[string]string{"branch": "main", "tier": "1"},
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
	got := render.RestoreWithBase(cur, base, nil, map[string]string{"ws": "main"})
	require.True(t, got.Partial)
	require.True(t, got.Safe)
}

func TestRestoreNoPartialWhenCountMatches(t *testing.T) {
	base := []byte(`{"a":"{{var.ws}}"}`)
	cur := []byte(`{"a":"main"}`)
	got := render.RestoreWithBase(cur, base, nil, map[string]string{"ws": "main"})
	require.False(t, got.Partial)
	require.Equal(t, `{"a":"{{var.ws}}"}`, string(got.Content))
}

// 内容里根本没出现的凭据/变量不影响任何标记。
func TestRestoreIgnoresAbsentValues(t *testing.T) {
	got := render.Restore(
		[]byte("普通内容"),
		map[string]string{"k": "sk-not-here-1234"},
		map[string]string{"ws": "nowhere"},
	)
	require.True(t, got.Safe)
	require.False(t, got.Partial)
	require.Equal(t, "普通内容", string(got.Content))
}

func TestRestoreOfEmptyContent(t *testing.T) {
	got := render.Restore(nil, map[string]string{"k": "sk-value-1234"}, nil)
	require.True(t, got.Safe)
	require.Empty(t, got.Content)
}
