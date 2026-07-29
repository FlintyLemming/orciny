package protocol_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := protocol.Hello{AgentVersion: "0.1.0", Fingerprint: "fp", ClientNonce: []byte{1, 2}}

	b, err := protocol.Encode(protocol.KindHello, nil, in)
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.Equal(t, protocol.KindHello, env.Kind)
	require.Nil(t, env.ID)

	out, err := protocol.DecodePayload[protocol.Hello](env)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

// 这两条 golden 断言锁死 wire 格式：keyasint 编号变了、omitempty 掉了，
// 都会在这里炸掉，而不是等到一个旧 agent 连不上才发现。
func TestWireFormatIsStable(t *testing.T) {
	b, err := protocol.Encode(protocol.KindHello, nil,
		protocol.Hello{AgentVersion: "0.1.0", Fingerprint: "fp", ClientNonce: []byte{1, 2}})
	require.NoError(t, err)
	require.Equal(t, "a2000102a30065302e312e300162667002420102", hex.EncodeToString(b))
}

func TestEnvelopeWithoutPayloadOmitsDataField(t *testing.T) {
	b, err := protocol.Encode(protocol.KindAuthResult, nil, nil)
	require.NoError(t, err)
	// a1 = 1 对键值；00 04 = Kind:4。ID 与 Data 都不出现。
	require.Equal(t, "a10004", hex.EncodeToString(b))
	require.Len(t, b, 3)
}

func TestEncodeWithRequestID(t *testing.T) {
	id := uint32(7)
	b, err := protocol.Encode(protocol.KindMachineInfo, &id, protocol.MachineInfo{Hostname: "h"})
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.NotNil(t, env.ID)
	require.Equal(t, uint32(7), *env.ID)
}

func TestDecodePayloadOnEmptyDataFails(t *testing.T) {
	b, err := protocol.Encode(protocol.KindAuthResult, nil, nil)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)

	_, err = protocol.DecodePayload[protocol.AuthResult](env)
	require.ErrorIs(t, err, protocol.ErrNoPayload)
}

func TestDecodeRejectsGarbage(t *testing.T) {
	_, err := protocol.Decode([]byte{0xff, 0xff, 0xff})
	require.Error(t, err)
}

// 未知 Kind 必须能被正常解出来——上层据此「记 warn 后丢弃」，
// 而不是解码失败导致断开（spec §3.2）。
func TestDecodePreservesUnknownKind(t *testing.T) {
	b, err := protocol.Encode(protocol.Kind(99), nil, nil)
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.Equal(t, protocol.Kind(99), env.Kind)
	require.False(t, env.Kind.IsKnown())
}

func TestKnownKinds(t *testing.T) {
	for _, k := range []protocol.Kind{
		protocol.KindHello, protocol.KindChallenge, protocol.KindAuth,
		protocol.KindAuthResult, protocol.KindMachineInfo,
	} {
		require.True(t, k.IsKnown(), "%d 应为已知 Kind", uint8(k))
		require.NotContains(t, k.String(), "unknown")
	}
	require.Equal(t, "unknown(99)", protocol.Kind(99).String())
}
