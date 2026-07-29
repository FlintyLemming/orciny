package routes_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/identity"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/routes"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

type fixture struct {
	app    *tests.TestApp
	srv    *httptest.Server
	svc    *enroll.Service
	ident  *identity.Store
	pubKey string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	ident := identity.NewStore(t.TempDir() + "/hub.pem")
	require.NoError(t, ident.Load())

	svc := enroll.NewService(app, events.NewWriter(app), clock.NewFake(time.Now()), 15*time.Minute, rand.Reader)

	router, err := apis.NewRouter(app)
	require.NoError(t, err)
	se := new(core.ServeEvent)
	se.App = app
	se.Router = router
	se.Server = &http.Server{}
	require.NoError(t, app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		if err := routes.Register(e, routes.Deps{Enroll: svc, Identity: ident, Version: "0.1.0"}); err != nil {
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

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	return &fixture{app: app, srv: srv, svc: svc, ident: ident, pubKey: protocol.EncodePublicKey(pub)}
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestEnrollTokensRequiresSuperuser(t *testing.T) {
	f := newFixture(t)
	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll-tokens", map[string]any{})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"签发 token 是管理动作，未认证必须被挡住")
}

func TestHubInfoRequiresSuperuser(t *testing.T) {
	f := newFixture(t)
	resp, err := http.Get(f.srv.URL + "/api/orciny/hub-info")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestEnrollHappyPathReturnsHubKey(t *testing.T) {
	f := newFixture(t)
	token, _, err := f.svc.IssueToken()
	require.NoError(t, err)

	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": token, "pubKey": f.pubKey,
		"hostname": "box", "os": "linux", "arch": "arm64", "agentVersion": "0.1.0",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		HubPubKey   string `json:"hubPubKey"`
		MachineID   string `json:"machineId"`
		Fingerprint string `json:"fingerprint"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, f.ident.PublicKeyBase64(), out.HubPubKey)
	require.NotEmpty(t, out.MachineID)
	require.Len(t, out.Fingerprint, 32)
}

func TestEnrollWithBadTokenIs401(t *testing.T) {
	f := newFixture(t)
	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": "无效", "pubKey": f.pubKey,
		"hostname": "box", "os": "linux", "arch": "arm64", "agentVersion": "0.1.0",
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestEnrollWithMissingFieldsIs400(t *testing.T) {
	f := newFixture(t)
	token, _, err := f.svc.IssueToken()
	require.NoError(t, err)

	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": token, "pubKey": f.pubKey,
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEnrollDoesNotLeakTokenInResponse(t *testing.T) {
	f := newFixture(t)
	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": "秘密token值", "pubKey": f.pubKey,
		"hostname": "box", "os": "linux", "arch": "arm64", "agentVersion": "0.1.0",
	})
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "秘密token值")
}
