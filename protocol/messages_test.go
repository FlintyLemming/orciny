package protocol_test

import (
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestAllM0MessagesRoundTrip(t *testing.T) {
	t.Run("hello", func(t *testing.T) {
		in := protocol.Hello{AgentVersion: "1.2.3", Fingerprint: "abc", ClientNonce: []byte("nonce")}
		env := mustDecode(t, mustEncode(t, protocol.KindHello, in))
		out, err := protocol.DecodePayload[protocol.Hello](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("challenge", func(t *testing.T) {
		in := protocol.Challenge{ServerNonce: []byte("sn"), HubSig: []byte("sig")}
		env := mustDecode(t, mustEncode(t, protocol.KindChallenge, in))
		out, err := protocol.DecodePayload[protocol.Challenge](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("auth", func(t *testing.T) {
		in := protocol.Auth{AgentSig: []byte("sig")}
		env := mustDecode(t, mustEncode(t, protocol.KindAuth, in))
		out, err := protocol.DecodePayload[protocol.Auth](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("auth_result_ok", func(t *testing.T) {
		in := protocol.AuthResult{OK: true}
		env := mustDecode(t, mustEncode(t, protocol.KindAuthResult, in))
		out, err := protocol.DecodePayload[protocol.AuthResult](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("auth_result_rejected", func(t *testing.T) {
		in := protocol.AuthResult{OK: false, Reason: "指纹未登记", Code: protocol.CodeUnknownFingerprint}
		env := mustDecode(t, mustEncode(t, protocol.KindAuthResult, in))
		out, err := protocol.DecodePayload[protocol.AuthResult](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("machine_info", func(t *testing.T) {
		in := protocol.MachineInfo{
			Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0",
			ToolVersions: map[string]string{"claude-code": "2.1.3"},
		}
		env := mustDecode(t, mustEncode(t, protocol.KindMachineInfo, in))
		out, err := protocol.DecodePayload[protocol.MachineInfo](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("machine_info_without_tools", func(t *testing.T) {
		// 机器上没装 Claude Code 是合法状态（spec §7.4）
		in := protocol.MachineInfo{Hostname: "box", OS: "darwin", Arch: "arm64", AgentVersion: "0.1.0"}
		env := mustDecode(t, mustEncode(t, protocol.KindMachineInfo, in))
		out, err := protocol.DecodePayload[protocol.MachineInfo](env)
		require.NoError(t, err)
		require.Nil(t, out.ToolVersions)
	})
}

// 未来版本给 MachineInfo 加字段时，旧版本必须能照常解出自己认识的字段。
// 这是 keyasint 的核心承诺，值得有一条测试盯着。
func TestUnknownFieldsAreIgnored(t *testing.T) {
	type futureMachineInfo struct {
		Hostname string            `cbor:"0,keyasint"`
		OS       string            `cbor:"1,keyasint"`
		Arch     string            `cbor:"2,keyasint"`
		AgentVer string            `cbor:"3,keyasint"`
		Tools    map[string]string `cbor:"4,keyasint,omitempty"`
		Kernel   string            `cbor:"9,keyasint"` // M9 才有的字段
	}
	raw, err := cbor.Marshal(futureMachineInfo{
		Hostname: "box", OS: "linux", Arch: "amd64", AgentVer: "9.9.9", Kernel: "6.1",
	})
	require.NoError(t, err)

	b, err := protocol.Encode(protocol.KindMachineInfo, nil, cbor.RawMessage(raw))
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)

	out, err := protocol.DecodePayload[protocol.MachineInfo](env)
	require.NoError(t, err)
	require.Equal(t, "box", out.Hostname)
	require.Equal(t, "9.9.9", out.AgentVersion)
}

func TestRejectCodesAreStable(t *testing.T) {
	// 原因码写进了 agent 的重试策略，改动等于改协议。
	require.Equal(t, uint8(1), protocol.CodeUnknownFingerprint)
	require.Equal(t, uint8(2), protocol.CodeBadSignature)
	require.Equal(t, uint8(3), protocol.CodeVersionTooOld)
	require.Equal(t, uint8(4), protocol.CodeMachineRemoved)
}

func mustEncode(t *testing.T, k protocol.Kind, payload any) []byte {
	t.Helper()
	b, err := protocol.Encode(k, nil, payload)
	require.NoError(t, err)
	return b
}

func mustDecode(t *testing.T, b []byte) protocol.Envelope {
	t.Helper()
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	return env
}
