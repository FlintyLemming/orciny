//go:build testing

package hub

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
)

// SeedConfigSet 建一个配置集、写入给定文件、发布 v1，返回 (setID, revID)。
//
// 放在 hub 公开包而不是 internal/testsupport：Go 的 internal 规则不允许
// testsupport（位于顶层 internal/）import hub/internal/*。testsupport 通过
// TestHub 转发到这里。
//
// files 的键是受管相对路径（以 HOME 为根），值是内容。权限位统一 0644；
// 需要 0600 的用例自行改写 revision。
func (h *Hub) SeedConfigSet(t *testing.T, name string, files map[string]string) (string, string) {
	t.Helper()

	b := blobs.New(h.App)
	ev := events.NewWriter(h.App)
	cs := configsets.NewService(h.App, b, ev)
	rs := revisions.NewService(h.App, b, ev)

	set, err := cs.Create(name, "由 testsupport 生成")
	require.NoError(t, err, "创建配置集")
	for path, content := range files {
		_, err := cs.SetDraftFile(set.Id, path, []byte(content), 0o644, nil)
		require.NoError(t, err, "写草稿 %s", path)
	}
	rev, err := rs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err, "发布")
	return set.Id, rev.Id
}

// SeedProvider 建一条 AI 服务配置（只配 claude 端点，key 内联在平台级），
// 返回 provider id。
func (h *Hub) SeedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()
	id, err := h.CreateProvider(providers.Input{
		Name: name,
		Key:  &key,
		Claude: providers.ClaudeEndpointInput{
			BaseURL:   baseURL,
			AuthField: providers.AuthToken,
			Models:    []providers.ClaudeModel{{Name: "glm-5.1"}, {Name: "glm-4.7"}},
		},
	})
	require.NoError(t, err, "建服务配置")
	return id
}

// SeedProviderWithOpenAI 建一条两个端点都配了的服务配置，返回 provider id。
// 平台级一把 key，两个端点共用——这是最常见的情形（M1.6 spec §2.3）。
func (h *Hub) SeedProviderWithOpenAI(
	t *testing.T, name, claudeURL, key, openaiURL, openaiModel string,
) string {
	t.Helper()
	id, err := h.CreateProvider(providers.Input{
		Name: name,
		Key:  &key,
		Claude: providers.ClaudeEndpointInput{
			BaseURL:   claudeURL,
			AuthField: providers.AuthToken,
			Models:    []providers.ClaudeModel{{Name: "glm-5.2"}, {Name: "glm-4.7"}},
		},
		OpenAI: providers.OpenAIEndpointInput{
			BaseURL:      openaiURL,
			AuthField:    providers.DefaultOpenAIAuthField,
			Models:       []string{openaiModel},
			DefaultModel: openaiModel,
		},
	})
	require.NoError(t, err, "建双端点服务配置")
	return id
}

// BindConfigSet 把配置集绑到给定 Provider（四槽同填 model）并发布一条新
// Revision，返回新 revision id。
func (h *Hub) BindConfigSet(t *testing.T, setID, providerID, model string) string {
	t.Helper()
	require.NoError(t, h.SetBinding(setID, &providers.Binding{
		Provider: providerID,
		Models: providers.ModelSlots{
			Main: model, Opus: model, Sonnet: model, Haiku: model,
		},
	}), "设置绑定")
	revID, err := h.PublishConfigSet(setID, "绑定服务配置")
	require.NoError(t, err, "发布")
	return revID
}

// ProviderInputOf 读回一条 Provider 的当前值，只把 claude 端点的 base_url
// 换成新的。三处 Key 都留 nil = 不修改，免得测试无意间把 key 冲掉。
func (h *Hub) ProviderInputOf(t *testing.T, providerID, baseURL string) providers.Input {
	t.Helper()
	r, err := h.provs.Get(providerID)
	require.NoError(t, err)
	cl := providers.ClaudeOf(r)
	oa := providers.OpenAIOf(r)
	return providers.Input{
		Name:   r.GetString("name"),
		Preset: r.GetString("preset"),
		Note:   r.GetString("note"),
		Claude: providers.ClaudeEndpointInput{
			BaseURL: baseURL, AuthField: cl.AuthField,
			Models: cl.Models, Defaults: cl.Defaults,
		},
		OpenAI: providers.OpenAIEndpointInput{
			BaseURL: oa.BaseURL, AuthField: oa.AuthField,
			Models: oa.Models, DefaultModel: oa.DefaultModel,
		},
	}
}

// RevisionBlob 读一条 Revision 里某个路径的 blob 内容。
// 用来断言「库里永远只有占位符，没有明文」。
func (h *Hub) RevisionBlob(t *testing.T, revID, path string) []byte {
	t.Helper()
	rs := revisions.NewService(h.App, blobs.New(h.App), events.NewWriter(h.App))
	files, err := rs.Files(revID)
	require.NoError(t, err)
	for _, f := range files {
		if f.Path == path {
			b, err := blobs.New(h.App).Get(f.Hash)
			require.NoError(t, err)
			return b
		}
	}
	t.Fatalf("版本 %s 里没有 %s", revID, path)
	return nil
}
