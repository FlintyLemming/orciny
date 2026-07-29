package handshake_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/protocol"
)

type mapStore map[string]*handshake.Machine

func (m mapStore) FindByFingerprint(fp string) (*handshake.Machine, error) {
	if v, ok := m[fp]; ok {
		return v, nil
	}
	return nil, handshake.ErrNotFound
}

type keySigner struct{ priv ed25519.PrivateKey }

func (k keySigner) Sign(msg []byte) []byte { return ed25519.Sign(k.priv, msg) }

type harness struct {
	server   *handshake.Server
	hubPub   ed25519.PublicKey
	agentPub ed25519.PublicKey
	agentKey ed25519.PrivateKey
	fp       string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	hubPub, hubPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	agentPub, agentPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	fp := protocol.Fingerprint(agentPub)
	store := mapStore{fp: {ID: "m1", PubKey: agentPub}}

	return &harness{
		server:   handshake.NewServer(store, keySigner{hubPriv}, semver.MustParse("0.1.0"), rand.Reader),
		hubPub:   hubPub,
		agentPub: agentPub,
		agentKey: agentPriv,
		fp:       fp,
	}
}

func (h *harness) hello(t *testing.T, version, fp string, nonce []byte) protocol.Envelope {
	t.Helper()
	b, err := protocol.Encode(protocol.KindHello, nil, protocol.Hello{
		AgentVersion: version, Fingerprint: fp, ClientNonce: nonce,
	})
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	return env
}

func decodeReply[T any](t *testing.T, reply []byte) T {
	t.Helper()
	env, err := protocol.Decode(reply)
	require.NoError(t, err)
	out, err := protocol.DecodePayload[T](env)
	require.NoError(t, err)
	return out
}

func TestHappyPath(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	clientNonce, err := protocol.NewNonce(rand.Reader)
	require.NoError(t, err)

	reply, out, err := ss.Handle(h.hello(t, "0.1.0", h.fp, clientNonce))
	require.NoError(t, err)
	require.Nil(t, out, "第一步不该有结论")
	require.False(t, ss.Done())

	ch := decodeReply[protocol.Challenge](t, reply)
	require.Len(t, ch.ServerNonce, protocol.NonceSize)
	require.True(t,
		ed25519.Verify(h.hubPub, protocol.HubSigPayload(clientNonce, ch.ServerNonce), ch.HubSig),
		"hub 必须对两个 nonce 签名")

	sig := ed25519.Sign(h.agentKey, protocol.AgentSigPayload(ch.ServerNonce, clientNonce))
	authBytes, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: sig})
	require.NoError(t, err)
	authEnv, err := protocol.Decode(authBytes)
	require.NoError(t, err)

	reply, out, err = ss.Handle(authEnv)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.True(t, out.OK)
	require.Equal(t, "m1", out.MachineID)
	require.Equal(t, h.fp, out.Fingerprint)
	require.True(t, ss.Done())

	res := decodeReply[protocol.AuthResult](t, reply)
	require.True(t, res.OK)
}

func TestUnknownFingerprintIsRejected(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	reply, out, err := ss.Handle(h.hello(t, "0.1.0", "从没见过的指纹1234567890123456", nonce))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, out.OK)
	require.Equal(t, protocol.CodeUnknownFingerprint, out.Code)
	require.True(t, ss.Done())

	res := decodeReply[protocol.AuthResult](t, reply)
	require.False(t, res.OK)
	require.Equal(t, protocol.CodeUnknownFingerprint, res.Code)
	require.NotEmpty(t, res.Reason, "要给排查者留一句人话")
}

func TestVersionTooOldIsRejectedBeforeLookup(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	// 版本检查在查库之前：指纹是合法的，但版本不够。
	_, out, err := ss.Handle(h.hello(t, "0.0.9", h.fp, nonce))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, protocol.CodeVersionTooOld, out.Code)
}

func TestUnparseableVersionIsRejected(t *testing.T) {
	h := newHarness(t)
	nonce, _ := protocol.NewNonce(rand.Reader)

	_, out, err := h.server.NewSession().Handle(h.hello(t, "不是版本号", h.fp, nonce))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, protocol.CodeVersionTooOld, out.Code)
}

func TestBadAgentSignatureIsRejected(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	_, _, err := ss.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.NoError(t, err)

	bad, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: bytes.Repeat([]byte{9}, 64)})
	require.NoError(t, err)
	env, err := protocol.Decode(bad)
	require.NoError(t, err)

	reply, out, err := ss.Handle(env)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, out.OK)
	require.Equal(t, protocol.CodeBadSignature, out.Code)
	require.Equal(t, h.fp, out.Fingerprint, "结论里要带指纹，否则事件写不出是谁")

	res := decodeReply[protocol.AuthResult](t, reply)
	require.Equal(t, protocol.CodeBadSignature, res.Code)
}

// 截获一次完整握手的 Auth，重连时重放 —— 必须失败，
// 因为 serverNonce 每个会话都新生成（spec §4.2）。
func TestReplayedAuthFails(t *testing.T) {
	h := newHarness(t)

	first := h.server.NewSession()
	nonce, _ := protocol.NewNonce(rand.Reader)
	reply, _, err := first.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.NoError(t, err)
	ch := decodeReply[protocol.Challenge](t, reply)

	capturedSig := ed25519.Sign(h.agentKey, protocol.AgentSigPayload(ch.ServerNonce, nonce))
	captured, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: capturedSig})
	require.NoError(t, err)
	capturedEnv, err := protocol.Decode(captured)
	require.NoError(t, err)

	// 新会话：同样的 clientNonce、同样的 Auth 报文
	second := h.server.NewSession()
	reply2, _, err := second.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.NoError(t, err)
	ch2 := decodeReply[protocol.Challenge](t, reply2)
	require.NotEqual(t, ch.ServerNonce, ch2.ServerNonce, "serverNonce 必须每次新生成")

	_, out, err := second.Handle(capturedEnv)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, out.OK)
	require.Equal(t, protocol.CodeBadSignature, out.Code)
}

func TestWrongMessageOrderIsProtocolError(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	// 上来就发 Auth
	b, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: make([]byte, 64)})
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)

	_, out, err := ss.Handle(env)
	require.Error(t, err, "顺序错乱是协议违规，由调用方直接断开")
	require.Nil(t, out)
}

func TestHandleAfterDoneIsError(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	_, _, err := ss.Handle(h.hello(t, "0.0.1", h.fp, nonce))
	require.NoError(t, err)
	require.True(t, ss.Done())

	_, _, err = ss.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.Error(t, err)
}

func TestShortClientNonceIsRejected(t *testing.T) {
	h := newHarness(t)
	_, out, err := h.server.NewSession().Handle(h.hello(t, "0.1.0", h.fp, []byte{1, 2, 3}))
	require.Error(t, err, "nonce 长度不对说明对面不是我们的 agent，直接断开")
	require.Nil(t, out)
}
