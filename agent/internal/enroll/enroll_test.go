package enroll_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	agentenroll "github.com/FlintyLemming/orciny/agent/internal/enroll"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

type stubHub struct {
	srv       *httptest.Server
	hubPub    ed25519.PublicKey
	calls     atomic.Int32
	lastBody  map[string]any
	failFirst bool
}

func newStubHub(t *testing.T) *stubHub {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	s := &stubHub{hubPub: pub}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/orciny/enroll", r.URL.Path)
		n := s.calls.Add(1)
		if s.failFirst && n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&s.lastBody))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"hubPubKey":   protocol.EncodePublicKey(s.hubPub),
			"machineId":   "m123",
			"fingerprint": "fp123",
		})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func opts(t *testing.T, hub *stubHub) agentenroll.Options {
	t.Helper()
	return agentenroll.Options{
		HubURL:       hub.srv.URL,
		Token:        "tok",
		IdentityDir:  filepath.Join(t.TempDir(), identity.DirName),
		AgentVersion: "0.1.0",
		RetryDelay:   0, // 测试不等待
	}
}

func TestRunGeneratesKeyAndPinsHubKey(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)

	out, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, "m123", out.MachineID)

	// 密钥已生成
	id, err := identity.Load(o.IdentityDir)
	require.NoError(t, err)
	require.Equal(t, id.Fingerprint(), out.Fingerprint,
		"返回的指纹应是本机公钥派生的，不是 hub 说了算")

	// hub 公钥已钉扎
	pinned, err := identity.LoadHubKey(o.IdentityDir)
	require.NoError(t, err)
	require.Equal(t, hub.hubPub, pinned)
	require.Equal(t, protocol.Fingerprint(hub.hubPub), out.HubKeyFP)

	// 请求体带了本机公钥与平台信息
	require.Equal(t, id.PublicKeyBase64(), hub.lastBody["pubKey"])
	require.NotEmpty(t, hub.lastBody["hostname"])
	require.NotEmpty(t, hub.lastBody["os"])
	require.NotEmpty(t, hub.lastBody["arch"])
	require.Equal(t, "0.1.0", hub.lastBody["agentVersion"])
}

func TestRunHonoursExpectedHubKey(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)
	o.ExpectHubKey = protocol.Fingerprint(hub.hubPub)

	_, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
}

func TestRunRejectsWrongHubKeyAndDoesNotPin(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)
	o.ExpectHubKey = "这是别的 hub 的指纹"

	_, err := agentenroll.Run(context.Background(), o)
	require.ErrorIs(t, err, agentenroll.ErrHubKeyMismatch)

	_, err = identity.LoadHubKey(o.IdentityDir)
	require.ErrorIs(t, err, identity.ErrNoHubKey,
		"带外校验失败时绝不能钉扎——那等于把中间人认成了 hub")
}

func TestRunRetriesOnTransientFailure(t *testing.T) {
	hub := newStubHub(t)
	hub.failFirst = true

	out, err := agentenroll.Run(context.Background(), opts(t, hub))
	require.NoError(t, err)
	require.Equal(t, "m123", out.MachineID)
	require.EqualValues(t, 2, hub.calls.Load(), "瞬时失败应重试")
}

func TestRunDoesNotRetryOn401(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := agentenroll.Run(context.Background(), agentenroll.Options{
		HubURL:       srv.URL,
		Token:        "tok",
		IdentityDir:  filepath.Join(t.TempDir(), identity.DirName),
		AgentVersion: "0.1.0",
		RetryDelay:   0,
	})
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load(), "token 无效是终局错误，重试没有意义")
}

func TestRunReusesExistingKey(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)

	first, err := identity.LoadOrCreate(o.IdentityDir)
	require.NoError(t, err)

	out, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, first.Fingerprint(), out.Fingerprint,
		"重新 enroll 不得换钥匙，否则会在 hub 侧变成一台新机器")
}

func TestRunTrimsTrailingSlashInHubURL(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)
	o.HubURL = hub.srv.URL + "/"

	_, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
}
