package providers_test

import (
	"encoding/json"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// 「端点配没配」= base_url 是否为空（spec §2.2）。
// 不加 enabled 布尔：两个字段表达同一件事只会产生
// 「enabled=true 但 base_url 为空」这种没有正确处理方式的中间态。
func TestConfiguredIsBaseURLPresence(t *testing.T) {
	require.False(t, providers.Endpoint{}.Configured())
	require.False(t, providers.Endpoint{AuthField: "ANTHROPIC_AUTH_TOKEN"}.Configured(),
		"只填了鉴权字段不算配置")
	require.True(t, providers.Endpoint{BaseURL: "https://x.example/anthropic"}.Configured())
}

func TestClaudeOfAndOpenAIOfDecodeRecord(t *testing.T) {
	app, _ := newTestApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", "智谱 GLM")
	r.Set("claude", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/anthropic",
		"auth_field":"ANTHROPIC_AUTH_TOKEN",
		"key_last4":"3456",
		"models":[{"name":"glm-5.2"}],
		"defaults":{"main":"glm-5.2","opus":"glm-5.2","sonnet":"glm-5.2","haiku":"glm-4.7"}
	}`))
	r.Set("openai", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/paas/v4",
		"auth_field":"OPENAI_API_KEY",
		"models":["glm-5.2"],
		"default_model":"glm-5.2"
	}`))
	require.NoError(t, app.Save(r))

	cl := providers.ClaudeOf(r)
	require.True(t, cl.Configured())
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", cl.BaseURL)
	require.Equal(t, "3456", cl.KeyLast4)
	require.Equal(t, "glm-4.7", cl.Defaults.Haiku)

	oa := providers.OpenAIOf(r)
	require.True(t, oa.Configured())
	require.Equal(t, "glm-5.2", oa.DefaultModel)
}

// 空 JSON 字段是正常情形（刚建的记录、004 之前的老记录），返回零值而不是炸。
func TestEndpointOfEmptyRecordIsZeroValue(t *testing.T) {
	app, _ := newTestApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", "空的")
	require.NoError(t, app.Save(r))

	require.False(t, providers.ClaudeOf(r).Configured())
	require.False(t, providers.OpenAIOf(r).Configured())
	require.Empty(t, providers.OpenAIOf(r).DefaultModel)
}

func TestClaudeModelJSONRoundTrip(t *testing.T) {
	m := providers.ClaudeModel{Name: "glm-5.2", OneM: true}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	require.Equal(t, `{"name":"glm-5.2","one_m":true}`, string(b))

	m2 := providers.ClaudeModel{Name: "glm-4.7", OneM: false}
	b2, err := json.Marshal(m2)
	require.NoError(t, err)
	require.Equal(t, `{"name":"glm-4.7"}`, string(b2))

	var decoded providers.ClaudeModel
	require.NoError(t, json.Unmarshal([]byte(`{"name":"test"}`), &decoded))
	require.Equal(t, "test", decoded.Name)
	require.False(t, decoded.OneM)
}
