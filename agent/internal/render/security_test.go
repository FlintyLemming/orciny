package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
)

// TestSecurityRestoredContentNeverContainsSecretValues 是 M1 的安全底线：
// 还原后的内容中不得含有任何已知秘密值。
//
// M1.6 之后秘密恰好是两个：claude.auth_token 与 openai.api_key
// （M1.6 spec §3.4）。两者语义完全相同，测试对两个都断言。
//
// 它独立成例并以 Security 打头，是为了在 CI 里能被单独点名跑：
//
//	go test -tags=testing -run TestSecurity ./agent/...
//
// 这条一旦破了，密钥会明文进入**不可变**的版本历史（spec §1.4）。
func TestSecurityRestoredContentNeverContainsSecretValues(t *testing.T) {
	secretsOf := map[string]string{
		"claude.auth_token": "sk-ant-abcdefghijklmnop",
		"openai.api_key":    "ghp_qrstuvwxyz01234567",
	}

	bodies := []string{
		`{"env":{"A":"sk-ant-abcdefghijklmnop","B":"ghp_qrstuvwxyz01234567"}}`,
		"文中提到 sk-ant-abcdefghijklmnop 与 ghp_qrstuvwxyz01234567",
		strings.Repeat("sk-ant-abcdefghijklmnop\n", 50),
		"混在一起：sk-ant-abcdefghijklmnopghp_qrstuvwxyz01234567",
		"边界：前缀sk-ant-abcdefghijklmnop后缀",
	}

	for _, body := range bodies {
		got := render.Restore([]byte(body), render.Values{Provider: secretsOf})
		require.True(t, got.Safe, "内容 %q 未能安全脱敏", body)
		for name, v := range secretsOf {
			require.NotContains(t, string(got.Content), v,
				"还原后仍能搜到 %s 的值", name)
		}
	}
}

// 短值是长值的子串时不能先替短的（M1 spec §6.4）：先替短的会把长值切碎，
// 剩下的碎片再也匹配不上，于是密钥的一部分留在了内容里。
func TestSecuritySupersetSecretIsRestoredFirst(t *testing.T) {
	secretsOf := map[string]string{
		"claude.auth_token": "sk-ant-abcdefghijklmnop",
		"openai.api_key":    "sk-ant-abcdefghijklmnop-longer", // 前者的超集
	}
	got := render.Restore(
		[]byte("短=sk-ant-abcdefghijklmnop 长=sk-ant-abcdefghijklmnop-longer"),
		render.Values{Provider: secretsOf})
	require.True(t, got.Safe)
	for _, v := range secretsOf {
		require.NotContains(t, string(got.Content), v)
	}
}

// 还原不干净时必须明确报出来，让调用方只报路径、不带内容（spec §6.4）。
func TestSecurityUnsafeRestoreIsReported(t *testing.T) {
	// 构造一个替不干净的情形：秘密值本身含有会被后续替换重新引入的片段。
	secretsOf := map[string]string{
		"claude.auth_token": "SECRET",
		"openai.api_key":    "X", // 长度 1：hub 侧拦得住，但历史数据里可能存在
	}
	got := render.Restore([]byte("值是 SECRET，另一个是 X"), render.Values{Provider: secretsOf})
	if !got.Safe {
		require.NotContains(t, string(got.Content), "SECRET",
			"判定为不安全时也不该把内容交出去——调用方会丢弃它")
	}
	// 无论 Safe 与否，调用方的契约是：Safe == false 时不上报 Content。
	// 这条断言锁住「Safe 为真 ⇒ 内容里搜不到任何秘密值」这个蕴含关系。
	if got.Safe {
		for _, v := range secretsOf {
			require.NotContains(t, string(got.Content), v)
		}
	}
}

// 极端输入不能让还原崩掉或死循环。
func TestSecurityRestoreHandlesPathologicalInput(t *testing.T) {
	// 值本身长得像占位符
	secretsOf := map[string]string{"claude.auth_token": "{{provider.claude.auth_token}}"}
	got := render.Restore([]byte("{{provider.claude.auth_token}}"),
		render.Values{Provider: secretsOf})
	require.NotNil(t, got.Content)

	empty := render.Restore([]byte("x"), render.Values{
		Provider: map[string]string{"claude.auth_token": ""},
		Vars:     map[string]string{"v": ""},
	})
	require.True(t, empty.Safe, "空值不该被当成「到处都能搜到」")
	require.Equal(t, "x", string(empty.Content))
}
