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
	segs, err := protocol.Parse([]byte("a{{cred.my-key_1}}b{{var.ws}}c{{machine.hostname}}"))
	require.NoError(t, err)

	var got []string
	for _, s := range segs {
		if s.Ref != nil {
			got = append(got, s.Ref.String())
		}
	}
	require.Equal(t, []string{"cred.my-key_1", "var.ws", "machine.hostname"}, got)
}

// 转义：CLAUDE.md 里讲模板语法时会出现字面的 "{{"，不给转义就没法管理这类文件。
func TestParseEscape(t *testing.T) {
	segs, err := protocol.Parse([]byte("写 {{{{cred.x}} 表示字面量"))
	require.NoError(t, err)
	var sb strings.Builder
	for _, s := range segs {
		require.Nil(t, s.Ref, "转义后不该产生引用")
		sb.WriteString(s.Text)
	}
	require.Equal(t, "写 {{cred.x}} 表示字面量", sb.String())
}

func TestEscapeLiteralRoundTrips(t *testing.T) {
	raw := "模板写法是 {{cred.x}}，两个花括号 {{ 也要转义"
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
		"{{cred.x",           // 未闭合
		"{{cred.}}",          // 空名
		"{{cred.a b}}",       // 非法字符
		"{{unknown.x}}",      // 未知前缀
		"{{cred}}",           // 缺 . 分隔
		"{{machine.secret}}", // machine 只认四个内置名
		"{{cred.{{x}}}}",     // 嵌套
	} {
		_, err := protocol.Parse([]byte(bad))
		require.ErrorIs(t, err, protocol.ErrBadPlaceholder, "%q 必须被拒", bad)
	}
}

func TestRefsAreDedupedAndSorted(t *testing.T) {
	refs, err := protocol.Refs([]byte("{{var.b}}{{cred.a}}{{var.b}}{{cred.a}}"))
	require.NoError(t, err)
	var got []string
	for _, r := range refs {
		got = append(got, r.String())
	}
	require.Equal(t, []string{"cred.a", "var.b"}, got)
}

func TestRenderFillsValues(t *testing.T) {
	segs, err := protocol.Parse([]byte(`{"key":"{{cred.k}}","ws":"{{var.w}}"}`))
	require.NoError(t, err)
	out, err := protocol.Render(segs, func(r protocol.Ref) (string, bool) {
		switch r.String() {
		case "cred.k":
			return "sk-secret", true
		case "var.w":
			return "main", true
		}
		return "", false
	})
	require.NoError(t, err)
	require.Equal(t, `{"key":"sk-secret","ws":"main"}`, string(out))
}

// 未定义引用绝不能渲染成字面 "{{cred.x}}" 落到 Claude Code 手里（spec §6.1）。
func TestRenderMissingRefIsAnError(t *testing.T) {
	segs, err := protocol.Parse([]byte("{{cred.gone}}"))
	require.NoError(t, err)
	_, err = protocol.Render(segs, func(protocol.Ref) (string, bool) { return "", false })
	require.Error(t, err)
	var missing *protocol.MissingRefError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "cred.gone", missing.Ref.String())
}

// 往返律：把任意内容 escape 之后 parse + render 回来必须一字不差。
// 这是整个凭据机制的正确性支点（spec §6.1 / §10.1）。
func TestParseRenderRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{"a", "b", "{", "}", "{{", "}}", "\n", "{{cred.x}}", "中文", " "}

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
