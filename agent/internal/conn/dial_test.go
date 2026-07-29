package conn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxzan/gws"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

// fakeHub 按脚本回应，用来制造真 hub 不会产生的坏情况。
type fakeHub struct {
	url        string
	priv       ed25519.PrivateKey
	signWith   ed25519.PrivateKey // 用它签名；与 priv 不同即「假 hub」
	rejectCode uint8
	gotInfo    chan protocol.MachineInfo
}

type fakeHandler struct {
	gws.BuiltinEventHandler
	h *fakeHub
}

func (f *fakeHandler) OnMessage(socket *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		return
	}
	switch env.Kind {
	case protocol.KindHello:
		hello, err := protocol.DecodePayload[protocol.Hello](env)
		if err != nil {
			return
		}
		serverNonce, _ := protocol.NewNonce(rand.Reader)
		b, _ := protocol.Encode(protocol.KindChallenge, nil, protocol.Challenge{
			ServerNonce: serverNonce,
			HubSig:      ed25519.Sign(f.h.signWith, protocol.HubSigPayload(hello.ClientNonce, serverNonce)),
		})
		_ = socket.WriteMessage(gws.OpcodeBinary, b)
	case protocol.KindAuth:
		res := protocol.AuthResult{OK: true}
		if f.h.rejectCode != 0 {
			res = protocol.AuthResult{OK: false, Code: f.h.rejectCode, Reason: "测试拒绝"}
		}
		b, _ := protocol.Encode(protocol.KindAuthResult, nil, res)
		_ = socket.WriteMessage(gws.OpcodeBinary, b)
	case protocol.KindMachineInfo:
		info, err := protocol.DecodePayload[protocol.MachineInfo](env)
		if err == nil {
			select {
			case f.h.gotInfo <- info:
			default:
			}
		}
	}
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	h := &fakeHub{priv: priv, signWith: priv, gotInfo: make(chan protocol.MachineInfo, 1)}
	up := gws.NewUpgrader(&fakeHandler{h: h}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := up.Upgrade(w, r)
		if err != nil {
			return
		}
		go socket.ReadLoop()
	}))
	t.Cleanup(srv.Close)

	h.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return h
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), identity.DirName))
	require.NoError(t, err)
	return id
}

func cfgFor(t *testing.T, h *fakeHub, id *identity.Identity) conn.Config {
	t.Helper()
	return conn.Config{
		HubURL:           h.url,
		Identity:         id,
		HubPub:           h.priv.Public().(ed25519.PublicKey),
		AgentVersion:     "0.1.0",
		Info:             protocol.MachineInfo{Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0"},
		HandshakeTimeout: 2 * time.Second,
		ReadTimeout:      2 * time.Second,
	}
}

func TestDialCompletesHandshakeAndSendsInfo(t *testing.T) {
	h := newFakeHub(t)
	s, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	select {
	case info := <-h.gotInfo:
		require.Equal(t, "box", info.Hostname)
	case <-time.After(2 * time.Second):
		t.Fatal("握手后未发送 MachineInfo")
	}
}

// hub 签名不匹配意味着中间人，或 hub 换了密钥。两种都需要人来判断，
// agent 必须立刻断开并报出来（spec §7.2）。
func TestDialRejectsWrongHubSignature(t *testing.T) {
	h := newFakeHub(t)
	_, other, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	h.signWith = other // 冒充者

	_, err = conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.ErrorIs(t, err, conn.ErrHubSignature)
}

func TestDialSurfacesRejectCode(t *testing.T) {
	h := newFakeHub(t)
	h.rejectCode = protocol.CodeUnknownFingerprint

	_, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.Error(t, err)

	var rej *conn.RejectedError
	require.ErrorAs(t, err, &rej)
	require.Equal(t, protocol.CodeUnknownFingerprint, rej.Code)
}

func TestDialTimesOutOnSilentHub(t *testing.T) {
	// 一个升级后什么都不回的 hub
	up := gws.NewUpgrader(&gws.BuiltinEventHandler{}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := up.Upgrade(w, r)
		if err != nil {
			return
		}
		go socket.ReadLoop()
	}))
	t.Cleanup(srv.Close)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	_, err = conn.Dial(context.Background(), conn.Config{
		HubURL:           "ws" + strings.TrimPrefix(srv.URL, "http"),
		Identity:         newIdentity(t),
		HubPub:           pub,
		AgentVersion:     "0.1.0",
		HandshakeTimeout: 200 * time.Millisecond,
		ReadTimeout:      time.Second,
	})
	require.Error(t, err, "hub 不吭声时必须超时返回，不能挂死")
}

func TestSessionDoneFiresOnHubClose(t *testing.T) {
	h := newFakeHub(t)
	s, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.NoError(t, err)

	require.NoError(t, s.Close())
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done 未在连接结束后关闭")
	}
}

func TestDialFailsOnUnreachableHub(t *testing.T) {
	_, err := conn.Dial(context.Background(), conn.Config{
		HubURL:           "ws://127.0.0.1:1",
		Identity:         newIdentity(t),
		HubPub:           make([]byte, ed25519.PublicKeySize),
		AgentVersion:     "0.1.0",
		HandshakeTimeout: time.Second,
		ReadTimeout:      time.Second,
	})
	require.Error(t, err)
}
