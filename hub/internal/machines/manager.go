package machines

import (
	"fmt"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// Manager 是连接注册表与在线状态机。
//
// 离线判定不是「连接一断就红」：直接在 WS 关闭时置 offline 会让面板在网络
// 抖动时不停闪红（spec §6.3）。这里的做法是断开后挂一个宽限定时器，
// 到期时若该指纹仍无连接才真的置 offline。
type Manager struct {
	app   core.App
	ev    *events.Writer
	clk   clock.Clock
	grace time.Duration

	mu      sync.Mutex
	conns   map[string]Conn        // fingerprint → 当前连接
	pending map[string]clock.Timer // fingerprint → 待定的离线定时器
	stopped bool

	// done 唤醒还卡在 timer.C() 上的等待协程，wg 让 Stop 能等它们真的收工。
	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewManager(app core.App, ev *events.Writer, clk clock.Clock, grace time.Duration) *Manager {
	return &Manager{
		app:     app,
		ev:      ev,
		clk:     clk,
		grace:   grace,
		conns:   make(map[string]Conn),
		pending: make(map[string]clock.Timer),
		done:    make(chan struct{}),
	}
}

// Register 登记一条握手成功的连接。
//
// 同一指纹已有连接时**新连接胜出**（agent 重启但 hub 还没检测到旧连接
// 断开时会发生，spec §6.4a）。旧连接以 close(1000, "replaced") 关闭，
// 它随后触发的 Unregister 会因为「注册表里已经不是它」而变成空操作。
func (m *Manager) Register(c Conn) error {
	fp := c.Fingerprint()

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("machines: 管理器已停止")
	}
	old, hadOld := m.conns[fp]
	m.conns[fp] = c
	if t, ok := m.pending[fp]; ok {
		t.Stop()
		delete(m.pending, fp)
	}
	m.mu.Unlock()

	if hadOld && old != c {
		if err := old.Close(1000, "replaced"); err != nil {
			m.app.Logger().Debug("关闭被顶替的连接时出错", "fingerprint", fp, "error", err)
		}
	}

	// 已经是 online 就不重复写库，也不重复发事件（重连在宽限期内的情形）。
	changed, err := m.setStatus(c.MachineID(), "online")
	if err != nil {
		// 登记失败要把连接从表里撤掉，否则会留下一条指向不存在记录的连接。
		m.mu.Lock()
		if m.conns[fp] == c {
			delete(m.conns, fp)
		}
		m.mu.Unlock()
		return err
	}
	if changed {
		if err := m.ev.Write(events.KindMachineConnected, c.MachineID(), map[string]any{
			"fingerprint": fp,
			"remote":      c.RemoteAddr(),
		}); err != nil {
			m.app.Logger().Warn("写 machine.connected 事件失败", "error", err)
		}
	}
	return nil
}

// Unregister 在连接关闭时调用。
func (m *Manager) Unregister(c Conn) {
	fp := c.Fingerprint()

	m.mu.Lock()
	// 关键判断（spec §6.3）：只有当注册表里这个指纹的当前连接确实是它，
	// 才算「这台机器断了」。否则说明它已被新连接顶替，什么都不该做。
	if cur, ok := m.conns[fp]; !ok || cur != c {
		m.mu.Unlock()
		return
	}
	delete(m.conns, fp)
	if m.stopped {
		m.mu.Unlock()
		return
	}

	machineID := c.MachineID()
	timer := m.clk.NewTimer(m.grace)
	m.pending[fp] = timer
	// Add 必须和 stopped 的检查在同一个临界区里：Stop 是先在锁内置位再在锁外
	// Wait，这样就不会出现「Wait 已经开始了还有人 Add」。
	m.wg.Add(1)
	m.mu.Unlock()

	go m.waitAndMarkOffline(fp, machineID, timer)
}

func (m *Manager) waitAndMarkOffline(fp, machineID string, timer clock.Timer) {
	defer m.wg.Done()

	select {
	case <-timer.C():
	case <-m.done:
		// 关停了。定时器已被 Stop 摘掉，再等下去就是永远。
		return
	}

	m.mu.Lock()
	// 定时器到期时若该指纹已有新连接，什么都不做。
	if cur, ok := m.pending[fp]; !ok || cur != timer {
		m.mu.Unlock()
		return
	}
	delete(m.pending, fp)
	if _, online := m.conns[fp]; online {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	changed, err := m.setStatus(machineID, "offline")
	if err != nil {
		m.app.Logger().Warn("置离线失败", "machine", machineID, "error", err)
		return
	}
	if changed {
		if err := m.ev.Write(events.KindMachineDisconnected, machineID, map[string]any{
			"fingerprint": fp,
		}); err != nil {
			m.app.Logger().Warn("写 machine.disconnected 事件失败", "error", err)
		}
	}
}

// UpdateInfo 落库握手后的首条业务消息。备注名是用户的，不动。
func (m *Manager) UpdateInfo(c Conn, info protocol.MachineInfo) error {
	r, err := m.app.FindRecordById("machines", c.MachineID())
	if err != nil {
		return fmt.Errorf("machines: 查询机器 %s: %w", c.MachineID(), err)
	}
	r.Set("hostname", info.Hostname)
	r.Set("os", info.OS)
	r.Set("arch", info.Arch)
	r.Set("agent_version", info.AgentVersion)
	if info.ToolVersions != nil {
		r.Set("tool_versions", info.ToolVersions)
	}
	if err := m.app.Save(r); err != nil {
		return fmt.Errorf("machines: 更新机器信息: %w", err)
	}
	return nil
}

// Get 返回某指纹当前的连接。
func (m *Manager) Get(fingerprint string) (Conn, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[fingerprint]
	return c, ok
}

// Count 返回在线连接数。
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conns)
}

// Stop 取消全部待定定时器并等待在途的置离线协程收工。多次调用是安全的。
//
// 「等」这一步不能省：定时器可能刚好已经触发，等待协程正走在写库路上。
// 只摘定时器就返回，会让那次写库落到已经关掉的数据库上。
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopped = true
	for fp, t := range m.pending {
		t.Stop()
		delete(m.pending, fp)
	}
	m.mu.Unlock()

	m.stopOnce.Do(func() { close(m.done) })
	m.wg.Wait()
}

// setStatus 写状态，返回状态是否真的变了。
// last_seen 只在这里写 —— 也就是只在状态变化时写（spec §6.5）。
func (m *Manager) setStatus(machineID, status string) (bool, error) {
	r, err := m.app.FindRecordById("machines", machineID)
	if err != nil {
		return false, fmt.Errorf("machines: 查询机器 %s: %w", machineID, err)
	}
	if r.GetString("status") == status {
		return false, nil
	}
	r.Set("status", status)
	r.Set("last_seen", m.clk.Now().UTC())
	if err := m.app.Save(r); err != nil {
		return false, fmt.Errorf("machines: 保存状态: %w", err)
	}
	return true, nil
}
