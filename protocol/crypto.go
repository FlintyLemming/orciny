package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// NonceSize 是挑战-应答里两个 nonce 的字节数。
const NonceSize = 32

// 域分隔前缀。一方的签名不能被拿去冒充另一方（spec §4.2）。
// 版本后缀（-v1）为将来更换签名格式留了余地：换格式就换前缀，
// 老 agent 的签名自动失效，不会被误认。
const (
	hubSigPrefix   = "orciny-hub-v1"
	agentSigPrefix = "orciny-agent-v1"
)

// NewNonce 生成一次性随机 nonce。生产传 crypto/rand.Reader。
// nonce 不入库、不复用。
func NewNonce(r io.Reader) ([]byte, error) {
	b := make([]byte, NonceSize)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, fmt.Errorf("protocol: 生成 nonce: %w", err)
	}
	return b, nil
}

// HubSigPayload 拼出 hub 要签名的字节串："orciny-hub-v1" ‖ clientNonce ‖ serverNonce。
func HubSigPayload(clientNonce, serverNonce []byte) []byte {
	return sigPayload(hubSigPrefix, clientNonce, serverNonce)
}

// AgentSigPayload 拼出 agent 要签名的字节串："orciny-agent-v1" ‖ serverNonce ‖ clientNonce。
//
// 注意两个 nonce 的顺序与 HubSigPayload 相反——即便前缀被抹掉，
// 两条负载也不会撞上。
func AgentSigPayload(serverNonce, clientNonce []byte) []byte {
	return sigPayload(agentSigPrefix, serverNonce, clientNonce)
}

func sigPayload(prefix string, first, second []byte) []byte {
	out := make([]byte, 0, len(prefix)+len(first)+len(second))
	out = append(out, prefix...)
	out = append(out, first...)
	out = append(out, second...)
	return out
}

// Fingerprint 由公钥派生机器指纹：base64url(SHA256(pubKey)) 取前 32 字符。
//
// 指纹是公钥的函数，不是硬件的函数（spec §4.3）：
//   - 消除「指纹对但公钥错」这种需要额外处理的中间状态；
//   - 规避硬件 ID 在 VPS 和容器里的重复（beszel 为此硬编码过 knownBadUUID）。
//
// 代价是重装系统或删掉 ~/.orciny/identity/ 会变成一台新机器——密钥丢了
// 本来就该重新建立信任。
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}

// EncodePublicKey 是公钥在 wire 与数据库里的统一表示：标准 base64 的裸公钥。
// enroll 请求体、machines.pub_key、agent 的 hub.pub 三处必须用同一种编码，
// 否则会出现「同一把钥匙、三种写法」的比对错误。
func EncodePublicKey(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// DecodePublicKey 是 EncodePublicKey 的逆操作，长度不符即拒绝。
func DecodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("protocol: 解码公钥: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("protocol: 公钥长度应为 %d 字节，实际 %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}
