package ws_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/lxzan/gws"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/hub/internal/ws"
	"github.com/FlintyLemming/orciny/protocol"
)

// —— 测试用的极简 WS 客户端 ——
// 只负责收发信封，握手逻辑由测试自己写，这样每一步都能单独出错。

type rawClient struct {
	conn   *gws.Conn
	inbox  chan protocol.Envelope
	closed chan struct{}
}

type rawHandler struct {
	gws.BuiltinEventHandler
	inbox  chan protocol.Envelope
	closed chan struct{}
}

func (h *rawHandler) OnMessage(_ *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		return
	}
	select {
	case h.inbox <- env:
	default:
	}
}

func (h *rawHandler) OnClose(*gws.Conn, error) {
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
}

func dial(t *testing.T, url string) *rawClient {
	t.Helper()
	h := &rawHandler{inbox: make(chan protocol.Envelope, 8), closed: make(chan struct{})}
	conn, _, err := gws.NewClient(h, &gws.ClientOption{Addr: url})
	require.NoError(t, err)
	go conn.ReadLoop()
	t.Cleanup(func() { _ = conn.WriteClose(1000, nil) })
	return &rawClient{conn: conn, inbox: h.inbox, closed: h.closed}
}

func (c *rawClient) send(t *testing.T, kind protocol.Kind, payload any) {
	t.Helper()
	b, err := protocol.Encode(kind, nil, payload)
	require.NoError(t, err)
	require.NoError(t, c.conn.WriteMessage(gws.OpcodeBinary, b))
}

func (c *rawClient) recv(t *testing.T) protocol.Envelope {
	t.Helper()
	select {
	case env := <-c.inbox:
		return env
	case <-time.After(2 * time.Second):
		t.Fatal("等待 hub 的响应超时")
		return protocol.Envelope{}
	}
}

// —— 服务端夹具 ——

type fixture struct {
	url      string
	agentKey ed25519.PrivateKey
	fp       string
	hubPub   ed25519.PublicKey
	registry *recordingRegistry
}

type recordingRegistry struct {
	machines.NopRegistry
	registered chan string
	infos      chan protocol.MachineInfo
}

func (r *recordingRegistry) Register(c machines.Conn) error {
	select {
	case r.registered <- c.Fingerprint():
	default:
	}
	return nil
}

func (r *recordingRegistry) UpdateInfo(_ machines.Conn, info protocol.MachineInfo) error {
	select {
	case r.infos <- info:
	default:
	}
	return nil
}

type mapStore map[string]*handshake.Machine

func (m mapStore) FindByFingerprint(fp string) (*handshake.Machine, error) {
	if v, ok := m[fp]; ok {
		return v, nil
	}
	return nil, handshake.ErrNotFound
}

type keySigner struct{ priv ed25519.PrivateKey }

func (k keySigner) Sign(msg []byte) []byte { return ed25519.Sign(k.priv, msg) }

func newFixture(t *testing.T, handshakeTimeout time.Duration) *fixture {
	t.Helper()

	hubPub, hubPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	agentPub, agentPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fp := protocol.Fingerprint(agentPub)

	reg := &recordingRegistry{
		registered: make(chan string, 4),
		infos:      make(chan protocol.MachineInfo, 4),
	}

	h := ws.NewHandler(ws.Deps{
		Handshake: handshake.NewServer(
			mapStore{fp: {ID: "m1", PubKey: agentPub}},
			keySigner{hubPriv},
			semver.MustParse("0.1.0"),
			rand.Reader,
		),
		Registry:          reg,
		HandshakeTimeout:  handshakeTimeout,
		ReadTimeout:       2 * time.Second,
		HeartbeatInterval: time.Hour, // 本任务不测心跳
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = h.UpgradeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	return &fixture{
		url:      "ws" + strings.TrimPrefix(srv.URL, "http"),
		agentKey: agentPriv,
		fp:       fp,
		hubPub:   hubPub,
		registry: reg,
	}
}

// —— 用例 ——

func TestFullHandshakeRegistersConnection(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: f.fp, ClientNonce: nonce,
	})

	env := c.recv(t)
	require.Equal(t, protocol.KindChallenge, env.Kind)
	ch, err := protocol.DecodePayload[protocol.Challenge](env)
	require.NoError(t, err)
	require.True(t, ed25519.Verify(f.hubPub, protocol.HubSigPayload(nonce, ch.ServerNonce), ch.HubSig))

	c.send(t, protocol.KindAuth, protocol.Auth{
		AgentSig: ed25519.Sign(f.agentKey, protocol.AgentSigPayload(ch.ServerNonce, nonce)),
	})

	env = c.recv(t)
	require.Equal(t, protocol.KindAuthResult, env.Kind)
	res, err := protocol.DecodePayload[protocol.AuthResult](env)
	require.NoError(t, err)
	require.True(t, res.OK)

	select {
	case fp := <-f.registry.registered:
		require.Equal(t, f.fp, fp)
	case <-time.After(2 * time.Second):
		t.Fatal("连接未被登记进注册表")
	}

	// 握手后的首条业务消息
	c.send(t, protocol.KindMachineInfo, protocol.MachineInfo{
		Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0",
	})
	select {
	case info := <-f.registry.infos:
		require.Equal(t, "box", info.Hostname)
	case <-time.After(2 * time.Second):
		t.Fatal("MachineInfo 未被处理")
	}
}

func TestUnknownFingerprintGetsCodeAndClose(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: "没登记过的指纹00000000000000", ClientNonce: nonce,
	})

	env := c.recv(t)
	res, err := protocol.DecodePayload[protocol.AuthResult](env)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Equal(t, protocol.CodeUnknownFingerprint, res.Code)

	select {
	case <-c.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("被拒绝后连接应当关闭")
	}
}

func TestVersionTooOldGetsCode(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.0.1", Fingerprint: f.fp, ClientNonce: nonce,
	})

	res, err := protocol.DecodePayload[protocol.AuthResult](c.recv(t))
	require.NoError(t, err)
	require.Equal(t, protocol.CodeVersionTooOld, res.Code)
}

func TestBadSignatureGetsCode(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: f.fp, ClientNonce: nonce,
	})
	_ = c.recv(t) // challenge

	c.send(t, protocol.KindAuth, protocol.Auth{AgentSig: make([]byte, ed25519.SignatureSize)})

	res, err := protocol.DecodePayload[protocol.AuthResult](c.recv(t))
	require.NoError(t, err)
	require.Equal(t, protocol.CodeBadSignature, res.Code)
}

// 升级后 N 毫秒不发 Auth，连接必须被关掉，防止半开连接堆积（spec §4.2）。
func TestHandshakeTimeoutClosesConnection(t *testing.T) {
	f := newFixture(t, 200*time.Millisecond)
	c := dial(t, f.url)

	select {
	case <-c.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("握手超时未生效")
	}
}

// 未知 Kind 记 warn 后丢弃，连接照常（spec §3.2）。
func TestUnknownKindIsIgnoredNotFatal(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: f.fp, ClientNonce: nonce,
	})
	ch, err := protocol.DecodePayload[protocol.Challenge](c.recv(t))
	require.NoError(t, err)

	// 塞一条未来版本才有的消息
	c.send(t, protocol.Kind(99), map[string]string{"x": "y"})

	// 连接仍然可用，握手照常完成
	c.send(t, protocol.KindAuth, protocol.Auth{
		AgentSig: ed25519.Sign(f.agentKey, protocol.AgentSigPayload(ch.ServerNonce, nonce)),
	})
	res, err := protocol.DecodePayload[protocol.AuthResult](c.recv(t))
	require.NoError(t, err)
	require.True(t, res.OK)

	select {
	case <-c.closed:
		t.Fatal("未知 Kind 不该导致断开")
	default:
	}
}

func TestGarbageFrameClosesConnection(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	require.NoError(t, c.conn.WriteMessage(gws.OpcodeBinary, []byte{0xff, 0xff, 0xff}))

	select {
	case <-c.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("无法解码的帧应当导致断开——对面不是我们的 agent")
	}
}

type recordingAgent struct {
	mu   sync.Mutex
	pull []string
	acks []protocol.ApplyAck
}

func (r *recordingAgent) Pull(machineID string, _ protocol.ConfigPull) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pull = append(r.pull, machineID)
}
func (r *recordingAgent) BlobRequest(string, protocol.BlobRequest) {}
func (r *recordingAgent) ApplyAck(_ string, a protocol.ApplyAck) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.acks = append(r.acks, a)
}
func (r *recordingAgent) DriftReport(string, protocol.DriftReport)     {}
func (r *recordingAgent) CollectResult(string, protocol.CollectResult) {}

func (r *recordingAgent) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pull), len(r.acks)
}

// newAuthedPair 起一个握手完成的连接，可选修改 Deps。
func newAuthedPair(t *testing.T, tweak func(*ws.Deps)) (*ws.Handler, *rawClient) {
	t.Helper()

	hubPub, hubPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_ = hubPub
	agentPub, agentPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fp := protocol.Fingerprint(agentPub)

	reg := &recordingRegistry{
		registered: make(chan string, 4),
		infos:      make(chan protocol.MachineInfo, 4),
	}
	d := ws.Deps{
		Handshake: handshake.NewServer(
			mapStore{fp: {ID: "m1", PubKey: agentPub}},
			keySigner{hubPriv},
			semver.MustParse("0.1.0"),
			rand.Reader,
		),
		Registry:          reg,
		HandshakeTimeout:  2 * time.Second,
		ReadTimeout:       2 * time.Second,
		HeartbeatInterval: time.Hour,
	}
	if tweak != nil {
		tweak(&d)
	}
	h := ws.NewHandler(d)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = h.UpgradeHTTP(w, r)
	}))
	t.Cleanup(func() {
		h.CloseAll()
		srv.Close()
		h.Wait()
	})

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c := dial(t, url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: fp, ClientNonce: nonce,
	})
	ch, err := protocol.DecodePayload[protocol.Challenge](c.recv(t))
	require.NoError(t, err)
	c.send(t, protocol.KindAuth, protocol.Auth{
		AgentSig: ed25519.Sign(agentPriv, protocol.AgentSigPayload(ch.ServerNonce, nonce)),
	})
	res, err := protocol.DecodePayload[protocol.AuthResult](c.recv(t))
	require.NoError(t, err)
	require.True(t, res.OK)

	select {
	case <-reg.registered:
	case <-time.After(2 * time.Second):
		t.Fatal("连接未被登记")
	}
	return h, c
}

// 已认证连接上收到 M1 的消息必须转给业务层，而不是落进
// 「已认证连接收到意外消息」的 default 分支。
func TestAuthenticatedMessagesAreRouted(t *testing.T) {
	rec := &recordingAgent{}
	h, client := newAuthedPair(t, func(d *ws.Deps) { d.Agent = rec })
	defer func() { _ = client.conn.WriteClose(1000, nil) }()

	client.send(t, protocol.KindConfigPull, protocol.ConfigPull{Have: "rev1"})
	client.send(t, protocol.KindApplyAck, protocol.ApplyAck{RevisionID: "rev1", OK: true})

	require.Eventually(t, func() bool {
		p, a := rec.counts()
		return p == 1 && a == 1
	}, 2*time.Second, 5*time.Millisecond)
	_ = h
}

// Agent 为 nil 时不能 panic：只关心握手的测试不必装配业务层。
func TestRoutingIsNoopWhenAgentIsNil(t *testing.T) {
	h, client := newAuthedPair(t, nil)
	defer func() { _ = client.conn.WriteClose(1000, nil) }()
	client.send(t, protocol.KindConfigPull, protocol.ConfigPull{})
	require.Eventually(t, func() bool { return h.LiveCount() == 1 },
		2*time.Second, 5*time.Millisecond, "连接不该因此断开")
}
