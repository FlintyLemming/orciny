package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/routes"
)

const providerReqBody = `{"name":"x","key":"sk-abcdef123456",` +
	`"claude":{"base_url":"https://a.test","auth_field":"ANTHROPIC_AUTH_TOKEN"}}`

// 全部管理端点必须要求 superuser（M0 spec §5.3 的纪律）。
func TestProviderRoutesRequireSuperuser(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/orciny/provider-presets", ""},
		{"POST", "/api/orciny/providers", providerReqBody},
		{"POST", "/api/orciny/providers/probe", `{"endpoint":"openai","base_url":"https://x.test"}`},
		{"PUT", "/api/orciny/providers/p1", providerReqBody},
		{"DELETE", "/api/orciny/providers/p1", ""},
		{"PUT", "/api/orciny/config-sets/s1/binding", `{"provider":"p1"}`},
		{"POST", "/api/orciny/config-sets/s1/fix-auth-field", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", c.method, c.path)
	}
}

// 预设表是编译期常量，端点只做序列化。
func TestProviderPresetsReturnsSeed(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	rec := doSuperuser(t, srv, "GET", "/api/orciny/provider-presets", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var got []providers.Preset
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, len(providers.Presets()))
	require.NotEmpty(t, got[0].Claude.BaseURL)
}

func TestCreateProviderReturnsID(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	rec := doSuperuser(t, srv, "POST", "/api/orciny/providers", providerReqBody)
	require.Equal(t, http.StatusOK, rec.Code)

	var got struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "p1", got.ID)
}

// 清空绑定用 PUT + {"provider":""}，与「设置」同一个端点，不另开 DELETE。
func TestSetBindingAcceptsEmptyProvider(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})

	rec := doSuperuser(t, srv, "PUT", "/api/orciny/config-sets/s1/binding",
		`{"provider":""}`)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, 1, admin.bindingCalls)
	require.Nil(t, admin.lastBinding, "空 provider 即解绑")

	rec = doSuperuser(t, srv, "PUT", "/api/orciny/config-sets/s1/binding",
		`{"provider":"p1","models":{"main":"glm-5.1","opus":"glm-5.1",`+
			`"sonnet":"glm-5.1","haiku":"glm-5.1"}}`)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, 2, admin.bindingCalls)
	require.NotNil(t, admin.lastBinding)
	require.Equal(t, "p1", admin.lastBinding.Provider)
	require.Equal(t, "glm-5.1", admin.lastBinding.Models.Haiku)
}

func TestDeleteProviderInUseMaps409(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{
		Admin: &fakeAdmin{providerDeleteErr: providers.ErrInUse},
	})
	rec := doSuperuser(t, srv, "DELETE", "/api/orciny/providers/p1", "")
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestFixAuthFieldReturnsNoContent(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	rec := doSuperuser(t, srv, "POST", "/api/orciny/config-sets/s1/fix-auth-field", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestBindingDriftRoutesRequireSuperuser(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/orciny/drift/e1/binding-match", ""},
		{"POST", "/api/orciny/drift/e1/rebind", `{"provider":"p1"}`},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", c.method, c.path)
	}
}

// 建服务配置时带 from_drift → 先把漂移里的 key 内联进 Input，再一次建成。
func TestCreateProviderWithFromDrift(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	body := `{"name":"Kimi 官方","preset":"kimi",` +
		`"claude":{"base_url":"https://api.moonshot.cn/anthropic",` +
		`"auth_field":"ANTHROPIC_AUTH_TOKEN"},` +
		`"from_drift":{"event":"e1","location":"env.ANTHROPIC_AUTH_TOKEN",` +
		`"endpoint":"claude"}}`

	rec := doSuperuser(t, srv, "POST", "/api/orciny/providers", body)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "e1", admin.fromDriftEvent)
	require.Equal(t, "env.ANTHROPIC_AUTH_TOKEN", admin.fromDriftLocation)
	require.Equal(t, "claude", admin.fromDriftEndpoint)
	require.Equal(t, "Kimi 官方", admin.fromDriftInput.Name)
	require.False(t, admin.plainCreateCalled, "带 from_drift 时不走普通的 CreateProvider")
}

func TestRebindRequiresProvider(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	rec := doSuperuser(t, srv, "POST", "/api/orciny/drift/e1/rebind", `{}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBindingMatchReturnsURL(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	rec := doSuperuser(t, srv, "GET", "/api/orciny/drift/e1/binding-match", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var got struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "https://api.moonshot.cn/anthropic", got.URL)
}

// 探测端点：粘一个根地址，后端试出正确路径并把模型清单带回来。
func TestProbeEndpointCompletesBaseURL(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"glm-5"},{"id":"kimi-k2.6"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><html></html>"))
	}))
	defer relay.Close()

	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	body := `{"endpoint":"openai","base_url":"` + relay.URL + `","key":"sk-test"}`
	rec := doSuperuser(t, srv, "POST", "/api/orciny/providers/probe", body)
	require.Equal(t, http.StatusOK, rec.Code)

	var got struct {
		Status  string   `json:"status"`
		BaseURL string   `json:"base_url"`
		Models  []string `json:"models"`
		Tried   []string `json:"tried"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "ok", got.Status)
	require.Equal(t, relay.URL+"/v1", got.BaseURL)
	require.Equal(t, []string{"glm-5", "kimi-k2.6"}, got.Models)
}

// 一个候选都没确认时仍是 200 —— 探测本身跑成功了，只是结论是「没找到」。
// base_url 留空，由前端保留用户的输入。
func TestProbeEndpointReportsNotFoundWithoutRewriting(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html>"))
	}))
	defer relay.Close()

	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	body := `{"endpoint":"claude","base_url":"` + relay.URL + `","key":"sk-test"}`
	rec := doSuperuser(t, srv, "POST", "/api/orciny/providers/probe", body)
	require.Equal(t, http.StatusOK, rec.Code)

	var got struct {
		Status  string `json:"status"`
		BaseURL string `json:"base_url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "not_api", got.Status)
	require.Empty(t, got.BaseURL)
}

// 解不动的地址不该让 hub 去发请求，直接 400。
func TestProbeEndpointRejectsBadBaseURL(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, body := range []string{
		`{"endpoint":"openai","base_url":"不是 URL","key":"k"}`,
		`{"endpoint":"openai","base_url":"","key":"k"}`,
		`{"endpoint":"火星协议","base_url":"https://x.test","key":"k"}`,
	} {
		rec := doSuperuser(t, srv, "POST", "/api/orciny/providers/probe", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", body)
	}
}

func TestCreateProviderWithClaudeModelPayload(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	body := `{"name":"智谱 GLM","claude":{"base_url":"https://open.bigmodel.cn/api/anthropic",` +
		`"auth_field":"ANTHROPIC_AUTH_TOKEN","models":[{"name":"glm-5.2","one_m":true},{"name":"glm-4.7"}]}}`

	rec := doSuperuser(t, srv, "POST", "/api/orciny/providers", body)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, admin.plainCreateCalled)
	require.Len(t, admin.lastCreateInput.Claude.Models, 2)
	require.Equal(t, "glm-5.2", admin.lastCreateInput.Claude.Models[0].Name)
	require.True(t, admin.lastCreateInput.Claude.Models[0].OneM)
	require.Equal(t, "glm-4.7", admin.lastCreateInput.Claude.Models[1].Name)
	require.False(t, admin.lastCreateInput.Claude.Models[1].OneM)
}

func TestProviderPresetsOutputsClaudeModels(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	rec := doSuperuser(t, srv, "GET", "/api/orciny/provider-presets", "")
	require.Equal(t, http.StatusOK, rec.Code)

	var got []struct {
		ID     string `json:"id"`
		Claude struct {
			Models []struct {
				Name string `json:"name"`
				OneM bool   `json:"one_m"`
			} `json:"models"`
		} `json:"claude"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	var zhipuFound bool
	for _, p := range got {
		if p.ID == "zhipu" {
			zhipuFound = true
			require.NotEmpty(t, p.Claude.Models)
			require.Equal(t, "glm-5.2", p.Claude.Models[0].Name)
			require.True(t, p.Claude.Models[0].OneM)
		}
	}
	require.True(t, zhipuFound)
}

