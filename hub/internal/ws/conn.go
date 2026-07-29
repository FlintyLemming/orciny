// Package ws 负责 WebSocket 升级、连接对象与读写循环。
// 握手的判断逻辑在 handshake 包，在线状态的判断逻辑在 machines 包——
// 这里只做「搬运」与「超时」。
package ws

import (
	"sync"

	"github.com/lxzan/gws"

	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/protocol"
)

// Conn 是一条已升级的连接。它结构性地满足 machines.Conn。
type Conn struct {
	socket  *gws.Conn
	session *handshake.Session

	mu          sync.RWMutex
	fingerprint string
	machineID   string
	authed      bool

	stopHeartbeat chan struct{}
	stopOnce      sync.Once
}

func (c *Conn) Fingerprint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fingerprint
}

func (c *Conn) MachineID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.machineID
}

func (c *Conn) RemoteAddr() string { return c.socket.RemoteAddr().String() }

func (c *Conn) authenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authed
}

func (c *Conn) markAuthed(fingerprint, machineID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fingerprint = fingerprint
	c.machineID = machineID
	c.authed = true
}

// Send 发一条信封。
func (c *Conn) Send(kind protocol.Kind, payload any) error {
	b, err := protocol.Encode(kind, nil, payload)
	if err != nil {
		return err
	}
	return c.socket.WriteMessage(gws.OpcodeBinary, b)
}

// SendAuthResult 发裁决消息。握手完成后再发 OK:false，即「授权已撤销」通知。
func (c *Conn) SendAuthResult(ok bool, reason string, code uint8) error {
	return c.Send(protocol.KindAuthResult, protocol.AuthResult{OK: ok, Reason: reason, Code: code})
}

// Close 关闭连接。
func (c *Conn) Close(code uint16, reason string) error {
	c.stopHeartbeatLoop()
	return c.socket.WriteClose(code, []byte(reason))
}

func (c *Conn) stopHeartbeatLoop() {
	c.stopOnce.Do(func() {
		if c.stopHeartbeat != nil {
			close(c.stopHeartbeat)
		}
	})
}
