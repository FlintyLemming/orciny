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

const providerReqBody = `{"name":"x","base_url":"https://a.test",` +
	`"auth_field":"ANTHROPIC_AUTH_TOKEN","credential":"c1"}`

// 全部管理端点必须要求 superuser（M0 spec §5.3 的纪律）。
func TestProviderRoutesRequireSuperuser(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/orciny/provider-presets", ""},
		{"POST", "/api/orciny/providers", providerReqBody},
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
	require.NotEmpty(t, got[0].BaseURL)
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

// 建服务配置时带 from_drift → 先抽 key 成凭据，再用它建。
func TestCreateProviderWithFromDrift(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	body := `{"name":"Kimi 官方","preset":"kimi",` +
		`"base_url":"https://api.moonshot.cn/anthropic",` +
		`"auth_field":"ANTHROPIC_AUTH_TOKEN",` +
		`"from_drift":{"event":"e1","location":"env.ANTHROPIC_AUTH_TOKEN","name":"kimi_key"}}`

	rec := doSuperuser(t, srv, "POST", "/api/orciny/providers", body)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, "e1", admin.fromDriftEvent)
	require.Equal(t, "env.ANTHROPIC_AUTH_TOKEN", admin.fromDriftLocation)
	require.Equal(t, "kimi_key", admin.fromDriftName)
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
