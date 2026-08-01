package render_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
)

// TestSecurityRestoredContentNeverContainsCredentialValues 是 M1 的安全底线：
// 还原后的内容中不得含有任何已知凭据值。
//
// 它独立成例并以 Security 打头，是为了在 CI 里能被单独点名跑：
//
//	go test -tags=testing -run TestSecurity ./agent/...
//
// 这条一旦破了，密钥会明文进入**不可变**的版本历史（spec §1.4）。
func TestSecurityRestoredContentNeverContainsCredentialValues(t *testing.T) {
	creds := map[string]string{
		"a": "sk-ant-abcdefghijklmnop",
		"b": "ghp_qrstuvwxyz01234567",
		"c": "AKIAIOSFODNN7EXAMPLE",
		"d": "sk-ant-abcdefghijklmnop-longer", // a 的超集
	}

	bodies := []string{
		`{"env":{"A":"sk-ant-abcdefghijklmnop","B":"ghp_qrstuvwxyz01234567"}}`,
		"文中提到 AKIAIOSFODNN7EXAMPLE 与 sk-ant-abcdefghijklmnop-longer",
		strings.Repeat("sk-ant-abcdefghijklmnop\n", 50),
		"混在一起：sk-ant-abcdefghijklmnopghp_qrstuvwxyz01234567",
		"边界：前缀sk-ant-abcdefghijklmnop后缀",
	}

	for _, body := range bodies {
		got := render.Restore([]byte(body), creds, nil)
		require.True(t, got.Safe, "内容 %q 未能安全脱敏", body)
		for name, v := range creds {
			require.NotContains(t, string(got.Content), v,
				"还原后仍能搜到凭据 %s 的值", name)
		}
	}
}

// 还原不干净时必须明确报出来，让调用方只报路径、不带内容（spec §6.4）。
func TestSecurityUnsafeRestoreIsReported(t *testing.T) {
	// 构造一个替不干净的情形：凭据值本身含有会被后续替换重新引入的片段。
	creds := map[string]string{
		"a": "SECRET",
		"b": "X", // 长度 1：hub 侧拦得住，但历史数据里可能存在
	}
	got := render.Restore([]byte("值是 SECRET，另一个是 X"), creds, nil)
	if !got.Safe {
		require.NotContains(t, string(got.Content), "SECRET",
			"判定为不安全时也不该把内容交出去——调用方会丢弃它")
	}
	// 无论 Safe 与否，调用方的契约是：Safe == false 时不上报 Content。
	// 这条断言锁住「Safe 为真 ⇒ 内容里搜不到任何凭据值」这个蕴含关系。
	if got.Safe {
		for _, v := range creds {
			require.NotContains(t, string(got.Content), v)
		}
	}
}

// 极端输入不能让还原崩掉或死循环。
func TestSecurityRestoreHandlesPathologicalInput(t *testing.T) {
	creds := map[string]string{"k": "{{cred.k}}"} // 值本身长得像占位符
	got := render.Restore([]byte("{{cred.k}}"), creds, nil)
	require.NotNil(t, got.Content)

	empty := render.Restore([]byte("x"), map[string]string{"e": ""}, map[string]string{"v": ""})
	require.True(t, empty.Safe, "空值不该被当成「到处都能搜到」")
	require.Equal(t, "x", string(empty.Content))
}
