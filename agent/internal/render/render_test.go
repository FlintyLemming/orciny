package render_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestRenderFillsFromSecrets(t *testing.T) {
	sec := &secrets.File{
		Provider: map[string]string{"claude.auth_token": "sk-ant-real"},
		Vars:     map[string]string{"ws": "main"},
		Machine:  map[string]string{"hostname": "mac-mini"},
	}
	out, err := render.Render(
		[]byte(`{"env":{"K":"{{provider.claude.auth_token}}"},`+
			`"ws":"{{var.ws}}","host":"{{machine.hostname}}"}`),
		sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, `{"env":{"K":"sk-ant-real"},"ws":"main","host":"mac-mini"}`, string(out))
}

// 未定义引用绝不能渲染成字面 {{var.x}} 落给 Claude Code（spec §6.1）。
func TestRenderRefusesUndefinedRef(t *testing.T) {
	sec := &secrets.File{Vars: map[string]string{}}
	_, err := render.Render([]byte(`{"K":"{{var.deleted}}"}`), sec.Lookup)
	require.Error(t, err)

	var missing *protocol.MissingRefError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "var.deleted", missing.Ref.String())
	require.NotContains(t, err.Error(), "sk-", "错误信息里不该出现任何值")
}

func TestRenderPassesThroughLiteralBraces(t *testing.T) {
	sec := &secrets.File{}
	out, err := render.Render([]byte("模板语法写作 {{{{var.x}}"), sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, "模板语法写作 {{var.x}}", string(out))
}

func TestRenderRejectsBadSyntax(t *testing.T) {
	sec := &secrets.File{}
	_, err := render.Render([]byte("{{var.x"), sec.Lookup)
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
}

// 渲染是纯函数：同样的输入两次得到同样的字节，
// 否则 rendered hash 会随机变化，每次对账都报漂移。
func TestRenderIsDeterministic(t *testing.T) {
	sec := &secrets.File{Vars: map[string]string{"a": "1", "b": "2", "c": "3"}}
	in := []byte("{{var.a}}{{var.b}}{{var.c}}{{var.a}}")
	first, err := render.Render(in, sec.Lookup)
	require.NoError(t, err)
	for range 20 {
		again, err := render.Render(in, sec.Lookup)
		require.NoError(t, err)
		require.Equal(t, first, again)
	}
}
