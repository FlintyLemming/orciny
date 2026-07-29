package protocol_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestNewNonceLengthAndUniqueness(t *testing.T) {
	a, err := protocol.NewNonce(rand.Reader)
	require.NoError(t, err)
	require.Len(t, a, protocol.NonceSize)
	require.Equal(t, 32, protocol.NonceSize)

	b, err := protocol.NewNonce(rand.Reader)
	require.NoError(t, err)
	require.NotEqual(t, a, b, "nonce 必须每次都不同")
}

func TestNewNonceFailsOnShortReader(t *testing.T) {
	_, err := protocol.NewNonce(bytes.NewReader([]byte{1, 2, 3}))
	require.Error(t, err, "熵不足时必须报错，绝不能返回半截 nonce")
}

func TestSigPayloadsCarryDomainPrefix(t *testing.T) {
	cn := bytes.Repeat([]byte{1}, protocol.NonceSize)
	sn := bytes.Repeat([]byte{2}, protocol.NonceSize)

	hub := protocol.HubSigPayload(cn, sn)
	agent := protocol.AgentSigPayload(sn, cn)

	require.True(t, bytes.HasPrefix(hub, []byte("orciny-hub-v1")))
	require.True(t, bytes.HasPrefix(agent, []byte("orciny-agent-v1")))
	require.Len(t, hub, len("orciny-hub-v1")+2*protocol.NonceSize)
	require.Len(t, agent, len("orciny-agent-v1")+2*protocol.NonceSize)
}

// 域分隔的意义：一方的签名不能被拿去冒充另一方（spec §4.2）。
func TestSigPayloadsAreNeverEqual(t *testing.T) {
	cn := bytes.Repeat([]byte{7}, protocol.NonceSize)
	sn := bytes.Repeat([]byte{7}, protocol.NonceSize) // 故意让两个 nonce 相同
	require.NotEqual(t, protocol.HubSigPayload(cn, sn), protocol.AgentSigPayload(sn, cn))
}

// 两个 nonce 都必须进签名：只用一个的话，另一方的挑战就是可预测的。
func TestSigPayloadChangesWithEitherNonce(t *testing.T) {
	cn := bytes.Repeat([]byte{1}, protocol.NonceSize)
	sn := bytes.Repeat([]byte{2}, protocol.NonceSize)
	other := bytes.Repeat([]byte{3}, protocol.NonceSize)

	base := protocol.HubSigPayload(cn, sn)
	require.NotEqual(t, base, protocol.HubSigPayload(other, sn))
	require.NotEqual(t, base, protocol.HubSigPayload(cn, other))
}

func TestSigPayloadIsVerifiableEd25519(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	cn, _ := protocol.NewNonce(rand.Reader)
	sn, _ := protocol.NewNonce(rand.Reader)

	sig := ed25519.Sign(priv, protocol.HubSigPayload(cn, sn))
	require.True(t, ed25519.Verify(pub, protocol.HubSigPayload(cn, sn), sig))
	require.False(t, ed25519.Verify(pub, protocol.AgentSigPayload(sn, cn), sig),
		"hub 的签名不能通过 agent 域的验证")
}

func TestFingerprintShapeAndDeterminism(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	fp := protocol.Fingerprint(pub)
	require.Len(t, fp, 32)
	require.Equal(t, fp, protocol.Fingerprint(pub), "同一公钥必须得到同一指纹")
	require.NotContains(t, fp, "+", "必须是 base64url 字母表")
	require.NotContains(t, fp, "/")
	require.NotContains(t, fp, "=")
}

func TestFingerprintDiffersPerKey(t *testing.T) {
	a, _, _ := ed25519.GenerateKey(rand.Reader)
	b, _, _ := ed25519.GenerateKey(rand.Reader)
	require.NotEqual(t, protocol.Fingerprint(a), protocol.Fingerprint(b))
}

func TestPublicKeyEncodingRoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	s := protocol.EncodePublicKey(pub)
	back, err := protocol.DecodePublicKey(s)
	require.NoError(t, err)
	require.Equal(t, pub, back)
}

func TestDecodePublicKeyRejectsWrongLength(t *testing.T) {
	_, err := protocol.DecodePublicKey("AAAA")
	require.Error(t, err, "长度不对的公钥必须拒绝，不能凑合")
}

func TestDecodePublicKeyRejectsNonBase64(t *testing.T) {
	_, err := protocol.DecodePublicKey("这不是 base64")
	require.Error(t, err)
}

func TestFingerprintGolden(t *testing.T) {
	// 全零公钥的指纹。这条锁死派生算法：换哈希、换编码、换截断长度都会炸。
	pub := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	require.Equal(t, "Zmh6rfhivXdsj8GLjp-OIAiXFIVu4jOz", protocol.Fingerprint(pub))
}
