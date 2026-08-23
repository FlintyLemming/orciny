package configsets_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// seedProvider 建一条只配了 claude 端点的 Provider，返回记录 id。
// 直接写记录而不走 providers.Store：configsets 的测试不该被 Store 的校验绑住。
func seedProvider(t *testing.T, app core.App, name string) string {
	t.Helper()
	return seedProviderWith(t, app, name, "")
}

// seedProviderWith 可以额外配上 openai 端点。openaiBaseURL 为空即不配。
func seedProviderWith(t *testing.T, app core.App, name, openaiBaseURL string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", name)
	p.Set("key_cipher", "x")
	p.Set("key_last4", "1234")
	p.Set("claude", providers.ClaudeEndpoint{
		Endpoint: providers.Endpoint{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: providers.AuthToken,
			KeyLast4:  "1234",
			Models:    []string{"glm-5.2"},
		},
	})
	p.Set("openai", providers.OpenAIEndpoint{
		Endpoint: providers.Endpoint{
			BaseURL:   openaiBaseURL,
			AuthField: providers.DefaultOpenAIAuthField,
			Models:    []string{},
		},
	})
	require.NoError(t, app.Save(p))
	return p.Id
}

func TestDraftBindingRoundTrip(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)

	// 未绑定：返回 (nil, nil)，不是错误。
	b, err := s.DraftBinding(set.Id)
	require.NoError(t, err)
	require.Nil(t, b)

	provID := seedProvider(t, app, "智谱 GLM · 个人")
	want := &providers.Binding{
		Provider: provID,
		Models: providers.ModelSlots{
			Main: "glm-5.1", Opus: "glm-5.1", Sonnet: "glm-5.1", Haiku: "glm-5.1",
		},
	}
	require.NoError(t, s.SetDraftBinding(set.Id, want))

	got, err := s.DraftBinding(set.Id)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSetDraftBindingNilClears(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))

	require.NoError(t, s.SetDraftBinding(set.Id, nil))
	got, err := s.DraftBinding(set.Id)
	require.NoError(t, err)
	require.Nil(t, got)
}

// 绑一条不存在的 Provider 必须当场被拒：让它进草稿，用户会在发布时
// 才发现，而那时错误信息离操作现场已经很远。
func TestSetDraftBindingRejectsUnknownProvider(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	err = s.SetDraftBinding(set.Id, &providers.Binding{Provider: "不存在"})
	require.ErrorIs(t, err, providers.ErrNotFound)
}

// 半填的四槽是配置错误（spec §2.3）：只钉主模型会让 Claude Code
// 拿 claude-haiku-* 去打人家的 endpoint。
func TestSetDraftBindingRejectsHalfFilledSlots(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	err = s.SetDraftBinding(set.Id, &providers.Binding{
		Provider: seedProvider(t, app, "智谱 GLM · 个人"),
		Models:   providers.ModelSlots{Main: "glm-5.1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "全空")
}
