package routes_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/importer"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/routes"
)

// fakeAdmin 记录调用并可注入错误。
type fakeAdmin struct {
	assigns   []assignCall
	deleteErr error
}

type assignCall struct {
	machine, set, mode string
}

func (f *fakeAdmin) AssignConfigSet(machineID, setID, mode string) error {
	f.assigns = append(f.assigns, assignCall{machineID, setID, mode})
	return nil
}
func (f *fakeAdmin) PublishConfigSet(string, string) (string, error) { return "r1", nil }
func (f *fakeAdmin) RollbackConfigSet(string, string) (string, error) {
	return "r2", nil
}
func (f *fakeAdmin) CreateCredential(string, string, string) error { return nil }
func (f *fakeAdmin) RotateCredential(string, string) error         { return nil }
func (f *fakeAdmin) DeleteCredential(string) error                 { return f.deleteErr }
func (f *fakeAdmin) StartImport(string) (string, string, error)    { return "tok", "set1", nil }
func (f *fakeAdmin) CloneConfigSet(string, string) (string, error) { return "new", nil }
func (f *fakeAdmin) DeleteConfigSet(string) error                  { return nil }
func (f *fakeAdmin) ImportFindings(string) ([]importer.Finding, error) {
	return nil, nil
}
func (f *fakeAdmin) ExtractCredential(string, string, string, string) error { return nil }
func (f *fakeAdmin) AdoptDrift([]string) (string, error)                    { return "rev-adopt", nil }
func (f *fakeAdmin) AdoptDriftReviewed([]string, []string) (string, error) {
	return "rev-adopt", nil
}
func (f *fakeAdmin) RestoreDrift([]string) error      { return nil }
func (f *fakeAdmin) IgnoreDrift([]string, bool) error { return nil }
func (f *fakeAdmin) ClearDegraded(string) error       { return nil }

func newRouterServer(t *testing.T, d routes.Deps) *httptest.Server {
	t.Helper()

	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	router, err := apis.NewRouter(app)
	require.NoError(t, err)
	se := new(core.ServeEvent)
	se.App = app
	se.Router = router
	se.Server = &http.Server{}
	require.NoError(t, app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		if err := routes.Register(e, d); err != nil {
			return err
		}
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		e.Server.Handler = mux
		return nil
	}))

	srv := httptest.NewServer(se.Server.Handler)
	t.Cleanup(srv.Close)
	// 把 app 挂到 Server 上供 doSuperuser 用——借用字段不优雅，改用返回值。
	srv.Config = &http.Server{Handler: se.Server.Handler}
	t.Cleanup(func() {})
	// 存到 t 上：通过返回闭包拿 token
	return withApp(srv, app)
}

// appServers 把 TestApp 与 Server 关联，供 doSuperuser 签发 token。
var appServers = map[*httptest.Server]*tests.TestApp{}

func withApp(srv *httptest.Server, app *tests.TestApp) *httptest.Server {
	appServers[srv] = app
	return srv
}

func doSuperuser(t *testing.T, srv *httptest.Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	app := appServers[srv]
	require.NotNil(t, app, "newRouterServer 必须登记 app")

	col, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	require.NoError(t, err)
	rec := core.NewRecord(col)
	// 每个请求用不同邮箱避免唯一约束冲突
	rec.SetEmail(strings.ReplaceAll(t.Name(), "/", "_") + "@orciny.test")
	rec.SetPassword("password123456")
	// 同一测试多次调用时可能已存在
	if err := app.Save(rec); err != nil {
		// 尝试按邮箱找回
		existing, findErr := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, rec.Email())
		require.NoError(t, findErr)
		rec = existing
	}
	token, err := rec.NewAuthToken()
	require.NoError(t, err)

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", token)
	recw := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(recw, req)
	return recw
}

// 全部管理端点必须要求 superuser（M0 spec §5.3 的纪律）。
func TestConfigRoutesRequireSuperuser(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/orciny/config-sets", `{"name":"x"}`},
		{"POST", "/api/orciny/config-sets/abc/publish", `{"note":"x"}`},
		{"POST", "/api/orciny/config-sets/abc/rollback", `{"revision":"r1"}`},
		{"POST", "/api/orciny/assignments", `{"machine":"m","config_set":"s","mode":"apply"}`},
		{"POST", "/api/orciny/credentials", `{"name":"k","value":"sk-12345678"}`},
		{"POST", "/api/orciny/machines/m/import", `{}`},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", c.method, c.path)
	}
}

// mode 必须显式给出，缺了就是 400 —— UI 上是强制二选一（spec §7.6）。
func TestAssignRejectsMissingMode(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	rec := doSuperuser(t, srv, "POST", "/api/orciny/assignments",
		`{"machine":"m","config_set":"s"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, admin.assigns)
}

// 业务错误映射成 4xx，而不是 500 —— 前端要拿它做提示。
func TestCredentialInUseMapsTo409(t *testing.T) {
	admin := &fakeAdmin{deleteErr: credentials.ErrInUse}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	rec := doSuperuser(t, srv, "DELETE", "/api/orciny/credentials/k", "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "引用")
}
