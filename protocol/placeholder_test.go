package protocol_test

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestParseLiteralOnly(t *testing.T) {
	segs, err := protocol.Parse([]byte("hello world"))
	require.NoError(t, err)
	require.Len(t, segs, 1)
	require.Nil(t, segs[0].Ref)
	require.Equal(t, "hello world", segs[0].Text)
}

func TestParseRefs(t *testing.T) {
	segs, err := protocol.Parse([]byte("a{{var.my-key_1}}b{{var.ws}}c{{machine.hostname}}"))
	require.NoError(t, err)

	var got []string
	for _, s := range segs {
		if s.Ref != nil {
			got = append(got, s.Ref.String())
		}
	}
	require.Equal(t, []string{"var.my-key_1", "var.ws", "machine.hostname"}, got)
}

// 转义：CLAUDE.md 里讲模板语法时会出现字面的 "{{"，不给转义就没法管理这类文件。
func TestParseEscape(t *testing.T) {
	segs, err := protocol.Parse([]byte("写 {{{{var.x}} 表示字面量"))
	require.NoError(t, err)
	var sb strings.Builder
	for _, s := range segs {
		require.Nil(t, s.Ref, "转义后不该产生引用")
		sb.WriteString(s.Text)
	}
	require.Equal(t, "写 {{var.x}} 表示字面量", sb.String())
}

func TestEscapeLiteralRoundTrips(t *testing.T) {
	raw := "模板写法是 {{var.x}}，两个花括号 {{ 也要转义"
	segs, err := protocol.Parse([]byte(protocol.EscapeLiteral(raw)))
	require.NoError(t, err)
	var sb strings.Builder
	for _, s := range segs {
		require.Nil(t, s.Ref)
		sb.WriteString(s.Text)
	}
	require.Equal(t, raw, sb.String())
}

func TestParseRejectsBadSyntax(t *testing.T) {
	for _, bad := range []string{
		"{{var.x",            // 未闭合
		"{{var.}}",           // 空名
		"{{var.a b}}",        // 非法字符
		"{{unknown.x}}",      // 未知前缀
		"{{var}}",            // 缺 . 分隔
		"{{machine.secret}}", // machine 只认四个内置名
		"{{var.{{x}}}}",      // 嵌套
	} {
		_, err := protocol.Parse([]byte(bad))
		require.ErrorIs(t, err, protocol.ErrBadPlaceholder, "%q 必须被拒", bad)
	}
}

func TestRefsAreDedupedAndSorted(t *testing.T) {
	refs, err := protocol.Refs([]byte("{{var.b}}{{machine.arch}}{{var.b}}{{machine.arch}}"))
	require.NoError(t, err)
	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	require.Equal(t, []string{"machine.arch", "var.b"}, got)
}

func TestRenderFillsValues(t *testing.T) {
	segs, err := protocol.Parse([]byte(`{"key":"{{var.k}}","ws":"{{var.w}}"}`))
	require.NoError(t, err)
	out, err := protocol.Render(segs, func(r protocol.Ref) (string, bool) {
		switch r.String() {
		case "var.k":
			return "sk-secret", true
		case "var.w":
			return "main", true
		}
		return "", false
	})
	require.NoError(t, err)
	require.Equal(t, `{"key":"sk-secret","ws":"main"}`, string(out))
}

// 未定义引用绝不能渲染成字面 "{{var.x}}" 落到 Claude Code 手里（spec §6.1）。
func TestRenderMissingRefIsAnError(t *testing.T) {
	segs, err := protocol.Parse([]byte("{{var.gone}}"))
	require.NoError(t, err)
	_, err = protocol.Render(segs, func(protocol.Ref) (string, bool) { return "", false })
	require.Error(t, err)
	var missing *protocol.MissingRefError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "var.gone", missing.Ref.String())
}

// 往返律：把任意内容 escape 之后 parse + render 回来必须一字不差。
// 这是整个秘密注入机制的正确性支点（spec §6.1 / §10.1）。
func TestParseRenderRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{"a", "b", "{", "}", "{{", "}}", "\n", "{{var.x}}", "中文", " "}

	for range 500 {
		var sb strings.Builder
		for range rng.IntN(20) {
			sb.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		raw := sb.String()

		segs, err := protocol.Parse([]byte(protocol.EscapeLiteral(raw)))
		require.NoError(t, err, "输入 %q", raw)
		out, err := protocol.Render(segs, func(protocol.Ref) (string, bool) { return "", false })
		require.NoError(t, err, "输入 %q", raw)
		require.Equal(t, raw, string(out), "往返不一致：%q", raw)
	}
}

func TestParseProviderRefs(t *testing.T) {
	segs, err := protocol.Parse([]byte(
		`{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}",` +
			`"ANTHROPIC_MODEL":"{{provider.claude.model}}"}}`))
	require.NoError(t, err)

	var got []string
	for _, s := range segs {
		if s.Ref != nil {
			require.Equal(t, protocol.RefProvider, s.Ref.Kind)
			got = append(got, s.Ref.String())
		}
	}
	require.Equal(t, []string{
		"provider.claude.base_url", "provider.claude.auth_token", "provider.claude.model",
	}, got)
}

// 与 machine.* 的既有测试对称：白名单外的名字必须被拒。
func TestParseRejectsUnknownProviderKey(t *testing.T) {
	_, err := protocol.Parse([]byte(`{{provider.claude.temperature}}`))
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
	require.Contains(t, err.Error(), "provider.claude.temperature")
}

func TestRefKindStringCoversProvider(t *testing.T) {
	require.Equal(t, "provider", protocol.RefProvider.String())
}

// Refs 提取要把 provider 一并去重排序吐出来——发布期靠它填 provider_keys。
func TestRefsIncludesProvider(t *testing.T) {
	refs, err := protocol.Refs([]byte(
		`{{provider.claude.model}}{{var.k}}{{provider.claude.model}}{{provider.claude.base_url}}`))
	require.NoError(t, err)
	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	require.Equal(t, []string{"provider.claude.base_url", "provider.claude.model", "var.k"}, got)
}

func TestProviderKeysAreEndpointQualified(t *testing.T) {
	require.Equal(t, []string{
		"claude.base_url", "claude.auth_token",
		"claude.model", "claude.model_opus", "claude.model_sonnet", "claude.model_haiku",
		"openai.base_url", "openai.api_key",
		"openai.model",
	}, protocol.ProviderKeys,
		"顺序即 UI 展示顺序，也是 agent 还原的 rankOf 定序依据，不要重排")
}

func TestParseAcceptsAllNineProviderKeys(t *testing.T) {
	for _, k := range protocol.ProviderKeys {
		segs, err := protocol.Parse([]byte("{{provider." + k + "}}"))
		require.NoError(t, err, "provider.%s 必须 parse 通过", k)
		require.Len(t, segs, 1)
		require.NotNil(t, segs[0].Ref)
		require.Equal(t, protocol.RefProvider, segs[0].Ref.Kind)
		require.Equal(t, k, segs[0].Ref.Name, "Ref.Name 承载两段名")
	}
}

// 旧写法不保留别名（spec §3.1）：本期是破坏性迁移。
func TestParseRejectsOldUnqualifiedProviderNames(t *testing.T) {
	for _, body := range []string{
		"provider.base_url", "provider.auth_token", "provider.model",
		"provider.model_opus", "provider.model_sonnet", "provider.model_haiku",
	} {
		_, err := protocol.Parse([]byte("{{" + body + "}}"))
		require.ErrorIs(t, err, protocol.ErrBadPlaceholder, "%s 必须被拒绝", body)
		require.Contains(t, err.Error(), "不是内置名")
	}
}

func TestParseRejectsCredPrefix(t *testing.T) {
	_, err := protocol.Parse([]byte("{{cred.zhipu}}"))
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
	require.Contains(t, err.Error(), "未知前缀")
}

func TestParseRejectsUnknownEndpointQualifiedName(t *testing.T) {
	for _, body := range []string{
		"provider.claude.temperature", // 端点对、字段不对
		"provider.gemini.base_url",    // 字段对、端点不对
		"provider.claude",             // 缺字段段
	} {
		_, err := protocol.Parse([]byte("{{" + body + "}}"))
		require.ErrorIs(t, err, protocol.ErrBadPlaceholder, "%s 必须被拒绝", body)
	}
}

// var 分支的字符集校验不动：var 是用户自定义名，仍需 validName。
func TestVarStillRejectsDotsInName(t *testing.T) {
	_, err := protocol.Parse([]byte("{{var.a.b}}"))
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
	require.Contains(t, err.Error(), "名字非法")
}

func TestEndpointOf(t *testing.T) {
	require.Equal(t, "claude", protocol.EndpointOf("claude.base_url"))
	require.Equal(t, "claude", protocol.EndpointOf("claude.model_haiku"))
	require.Equal(t, "openai", protocol.EndpointOf("openai.api_key"))
	require.Equal(t, "", protocol.EndpointOf("base_url"), "旧写法没有端点段")
	require.Equal(t, "", protocol.EndpointOf("gemini.base_url"), "不在白名单里")
	require.Equal(t, "", protocol.EndpointOf(""))
}

func TestRefsDeduplicatesAcrossBothEndpoints(t *testing.T) {
	content := []byte(`{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.claude.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.claude.auth_token}}",
    "OPENAI_BASE_URL": "{{provider.openai.base_url}}",
    "OPENAI_API_KEY": "{{provider.openai.api_key}}",
    "AGAIN": "{{provider.claude.base_url}}"
  }
}`)
	refs, err := protocol.Refs(content)
	require.NoError(t, err)

	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	// 去重 + 按 String() 升序
	require.Equal(t, []string{
		"provider.claude.auth_token",
		"provider.claude.base_url",
		"provider.openai.api_key",
		"provider.openai.base_url",
	}, got)
}

func TestRenderRoundTripWithBothEndpoints(t *testing.T) {
	content := []byte(`base={{provider.claude.base_url}} key={{provider.openai.api_key}}`)
	segs, err := protocol.Parse(content)
	require.NoError(t, err)
	out, err := protocol.Render(segs, func(r protocol.Ref) (string, bool) {
		switch r.Name {
		case "claude.base_url":
			return "https://open.bigmodel.cn/api/anthropic", true
		case "openai.api_key":
			return "sk-openai-abcdef", true
		}
		return "", false
	})
	require.NoError(t, err)
	require.Equal(t,
		"base=https://open.bigmodel.cn/api/anthropic key=sk-openai-abcdef",
		string(out))
}
