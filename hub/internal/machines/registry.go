// Package machines 维护连接注册表与机器的在线状态机。
//
// 依赖方向：ws 依赖 machines，machines 不依赖 ws。因此这里的 Conn 是接口，
// *ws.Conn 结构性地满足它。这样 machines 的单测可以用假连接推演时序，
// 不必真的开一个 WebSocket。
package machines

import "github.com/FlintyLemming/orciny/protocol"

// Conn 是注册表眼中的一条连接。
type Conn interface {
	Fingerprint() string
	MachineID() string
	RemoteAddr() string
	// Send 发一条任意消息。M1 的下发链路走它（ConfigNotify / ConfigSnapshot /
	// BlobData / DriftCommand / CollectRequest）。
	Send(kind protocol.Kind, payload any) error
	// SendAuthResult 用于事后的「授权已撤销」通知（spec §3.3）。
	SendAuthResult(ok bool, reason string, code uint8) error
	Close(code uint16, reason string) error
}

// Registry 是 ws 对注册表的全部需求。计划 6 由 *Manager 实现。
type Registry interface {
	// Register 在握手成功后登记连接。同一指纹已有连接时，新连接胜出。
	Register(c Conn) error
	// Unregister 在连接关闭时调用。是否真的判定离线由实现决定（5 秒宽限）。
	Unregister(c Conn)
	// UpdateInfo 处理握手后的首条业务消息。
	UpdateInfo(c Conn, info protocol.MachineInfo) error
}

// NopRegistry 什么都不做，供计划 5 的 ws 在 Manager 就位之前使用，
// 以及供只关心握手的测试使用。
type NopRegistry struct{}

func (NopRegistry) Register(Conn) error                         { return nil }
func (NopRegistry) Unregister(Conn)                             {}
func (NopRegistry) UpdateInfo(Conn, protocol.MachineInfo) error { return nil }
