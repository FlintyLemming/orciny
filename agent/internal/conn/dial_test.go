package conn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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
	// revokeCode 非零时，收到 MachineInfo 后补发一条 AuthResult{OK:false}
	// 再关连接——即 spec §3.3 的「授权已撤销」通知（M0 唯一触发场景是
	// 机器在面板上被删）。
	revokeCode uint8
	gotInfo    chan protocol.MachineInfo

	mu     sync.Mutex
	socket *gws.Conn
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
		f.h.mu.Lock()
		f.h.socket = socket
		f.h.mu.Unlock()
		info, err := protocol.DecodePayload[protocol.MachineInfo](env)
		if err == nil {
			select {
			case f.h.gotInfo <- info:
			default:
			}
		}
		if f.h.revokeCode != 0 {
			b, _ := protocol.Encode(protocol.KindAuthResult, nil, protocol.AuthResult{
				OK: false, Code: f.h.revokeCode, Reason: "该机器已在面板中被删除，请重新 enroll",
			})
			_ = socket.WriteMessage(gws.OpcodeBinary, b)
			_ = socket.WriteClose(1000, []byte("machine removed"))
		}
	}
}

// push 往已握手的连接上写一帧，供连接期消息路由测试使用。
func (h *fakeHub) push(t *testing.T, kind protocol.Kind, payload any) {
	t.Helper()
	require.Eventually(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.socket != nil
	}, 2*time.Second, 5*time.Millisecond, "等待 agent 完成握手")
	h.mu.Lock()
	socket := h.socket
	h.mu.Unlock()
	b, err := protocol.Encode(kind, nil, payload)
	require.NoError(t, err)
	require.NoError(t, socket.WriteMessage(gws.OpcodeBinary, b))
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

// 握手完成之后 hub 仍可发 AuthResult{OK:false} 作为「授权已撤销」通知
// （spec §3.3），M0 的唯一触发场景是机器在面板上被删（§6.4c）。
// agent 必须把它当成连接结束的原因，而不是一次普通断开——否则重连循环
// 只会看到「连接断开了」，继续无限重连（验收第 8 条就是这么挂的）。
func TestSessionSurfacesRevocationAfterHandshake(t *testing.T) {
	h := newFakeHub(t)
	h.revokeCode = protocol.CodeMachineRemoved

	s, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.NoError(t, err, "握手本身应当成功，撤销是握手之后的事")

	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("收到撤销通知后 Done 未关闭")
	}

	var rej *conn.RejectedError
	require.ErrorAs(t, s.Err(), &rej, "连接结束原因必须是 RejectedError，才能走 §7.2 的分流")
	require.Equal(t, protocol.CodeMachineRemoved, rej.Code)
}

// 连接期收到 OK:true 没有意义，但不能因此崩掉或结束连接。
func TestSessionIgnoresPositiveAuthResultAfterHandshake(t *testing.T) {
	h := newFakeHub(t)
	h.revokeCode = 0

	s, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// 等 hub 收到 MachineInfo，确认连接确实进入了「连接期」
	select {
	case <-h.gotInfo:
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 MachineInfo")
	}

	select {
	case <-s.Done():
		t.Fatal("连接不该结束")
	case <-time.After(200 * time.Millisecond):
	}
	require.NoError(t, s.Err())
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

// hub 与 agent 的读上限必须同源。M0 只设了 hub 侧，agent 走库默认值，
// 而 M1 的 ConfigSnapshot / BlobData 会逼近 1 MiB（spec §5.4）。
func TestClientReadMaxPayloadMatchesProtocol(t *testing.T) {
	require.Equal(t, 1<<20, protocol.MaxPayload)
	require.Equal(t, int(protocol.MaxPayload), conn.ClientReadMaxPayloadSize())
}

// 连接期收到的业务消息要交给上层，而不是落进「忽略意外消息」的分支。
// M0 的 AC 修正（inbox 无人消费）就是这个位置出的问题。
func TestConnectedMessagesReachOnMessage(t *testing.T) {
	got := make(chan protocol.Envelope, 4)
	h := newFakeHub(t)
	cfg := cfgFor(t, h, newIdentity(t))
	cfg.OnMessage = func(env protocol.Envelope) { got <- env }

	sess, err := conn.Dial(context.Background(), cfg)
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()

	h.push(t, protocol.KindConfigNotify, protocol.ConfigNotify{ConfigSetID: "set1"})

	select {
	case env := <-got:
		require.Equal(t, protocol.KindConfigNotify, env.Kind)
	case <-time.After(2 * time.Second):
		t.Fatal("OnMessage 未收到 ConfigNotify")
	}
}

// AuthResult 仍走原来的撤销通知路径，不该被 OnMessage 抢走。
func TestAuthResultStillEndsSession(t *testing.T) {
	h := newFakeHub(t)
	cfg := cfgFor(t, h, newIdentity(t))
	cfg.OnMessage = func(protocol.Envelope) { t.Error("AuthResult 不该走 OnMessage") }

	sess, err := conn.Dial(context.Background(), cfg)
	require.NoError(t, err)
	h.push(t, protocol.KindAuthResult,
		protocol.AuthResult{OK: false, Code: protocol.CodeMachineRemoved, Reason: "已删除"})

	select {
	case <-sess.Done():
		var rej *conn.RejectedError
		require.ErrorAs(t, sess.Err(), &rej)
		require.Equal(t, protocol.CodeMachineRemoved, rej.Code)
	case <-time.After(2 * time.Second):
		t.Fatal("连接未因撤销通知结束")
	}
}
