package protocol

// M0 的全部消息（spec §3.3）。
//
// 字段编号同样只增不改不复用。加字段用新编号并带 omitempty，
// 老版本解码时会忽略它——这是选 CBOR + keyasint 的主要收益。

// Hello 是 agent 连上来的第一条消息。
type Hello struct {
	AgentVersion string `cbor:"0,keyasint"`
	Fingerprint  string `cbor:"1,keyasint"`
	ClientNonce  []byte `cbor:"2,keyasint"` // NonceSize 字节
}

// Challenge 是 hub 的应答：自己的 nonce + 对两个 nonce 的签名。
type Challenge struct {
	ServerNonce []byte `cbor:"0,keyasint"` // NonceSize 字节
	HubSig      []byte `cbor:"1,keyasint"` // Ed25519 签名，见 HubSigPayload
}

// Auth 是 agent 对 hub 挑战的应答。
type Auth struct {
	AgentSig []byte `cbor:"0,keyasint"` // Ed25519 签名，见 AgentSigPayload
}

// AuthResult 是 hub 的裁决。
//
// 它有一个额外用途（spec §3.3）：握手完成后 hub 仍可发 OK:false 作为
// 「授权已撤销」通知，随后关闭连接。agent 在任何时候收到 OK:false 都按
// Code 走同一条处理路径，不区分握手期还是连接期。
type AuthResult struct {
	OK     bool   `cbor:"0,keyasint"`
	Reason string `cbor:"1,keyasint,omitempty"` // 人类可读，供日志
	Code   uint8  `cbor:"2,keyasint,omitempty"` // 机器可读，驱动重试策略
}

// MachineInfo 是握手后的首条业务消息，hub 据此更新机器记录。
type MachineInfo struct {
	Hostname     string            `cbor:"0,keyasint"`
	OS           string            `cbor:"1,keyasint"` // linux / darwin
	Arch         string            `cbor:"2,keyasint"` // amd64 / arm64
	AgentVersion string            `cbor:"3,keyasint"`
	ToolVersions map[string]string `cbor:"4,keyasint,omitempty"` // {"claude-code": "2.1.3"}
}

// 拒绝原因码（spec §4.6）。
//
// 给出明确原因而不是笼统的 401，是因为这几种情况的正确处置完全不同，
// 而排查者手上往往只有 agent 的日志。
const (
	CodeUnknownFingerprint uint8 = 1 // 提示重新 enroll，5 分钟间隔重试
	CodeBadSignature       uint8 = 2 // 视为异常，5 分钟间隔重试
	CodeVersionTooOld      uint8 = 3 // 5 分钟间隔重试（等 hub 升级或 agent 更新）
	CodeMachineRemoved     uint8 = 4 // 停止重试，需人工重新 enroll
)
