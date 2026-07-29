// Package protocol 是 hub 与 agent 唯一的公共依赖（spec §2.2 第 2 条）。
//
// 纪律：本包只放数据结构、编解码、以及两侧必须逐位一致的密码学格式
// （nonce 长度、签名负载拼装、指纹派生）。任何业务逻辑、任何对
// PocketBase 的引用、任何对 hub/agent 包的引用都不允许出现在这里。
package protocol

import "fmt"

// Kind 标识信封里装的是哪种消息。
//
// 纪律（spec §3.2）：
//   - 编号只增不改不复用。删除某种消息时，注释掉并保留编号。
//   - 10-19 预留给 M1 配置下发（ConfigNotify / ConfigPull / ApplyAck /
//     DriftReport / DriftRestore）
//   - 20-29 预留给 M2 数据面（UsageBatch / CollectorReport）
//   - 30-39 预留给 agent 生命周期（AgentUpdate）
//   - 收到未知 Kind：记 warn 日志后丢弃，**不断开连接**。这样新版 hub 给
//     旧版 agent 发新消息时，旧 agent 只是忽略而不是重连风暴。
type Kind uint8

const (
	KindHello       Kind = 1 // agent → hub
	KindChallenge   Kind = 2 // hub   → agent
	KindAuth        Kind = 3 // agent → hub
	KindAuthResult  Kind = 4 // hub   → agent（握手结果，亦用作事后的授权撤销通知）
	KindMachineInfo Kind = 5 // agent → hub
)

// IsKnown 报告本版本是否认识这个 Kind。分派前先问它。
func (k Kind) IsKnown() bool {
	switch k {
	case KindHello, KindChallenge, KindAuth, KindAuthResult, KindMachineInfo:
		return true
	default:
		return false
	}
}

func (k Kind) String() string {
	switch k {
	case KindHello:
		return "hello"
	case KindChallenge:
		return "challenge"
	case KindAuth:
		return "auth"
	case KindAuthResult:
		return "auth_result"
	case KindMachineInfo:
		return "machine_info"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}
