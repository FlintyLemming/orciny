package ws

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/lxzan/gws"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// Deps 是 ws 需要的全部依赖。
type Deps struct {
	App       core.App
	Handshake *handshake.Server
	Registry  machines.Registry
	Events    *events.Writer
	Clock     clock.Clock

	HandshakeTimeout  time.Duration
	ReadTimeout       time.Duration
	HeartbeatInterval time.Duration
}

// Handler 是 WS 端点。一个 hub 一个实例。
//
// 它只为「关停」保留每连接状态：live 是当前活着的读循环，wg 用来等它们退出。
// 业务意义上的连接注册表在 machines 包，与这里无关。
type Handler struct {
	d        Deps
	upgrader *gws.Upgrader
	log      *slog.Logger

	mu   sync.Mutex
	live map[*gws.Conn]struct{}
	wg   sync.WaitGroup
}

const sessionKey = "orciny.conn"

// 兜底值。生产走 hub.Config.WithDefaults，这里只防「Deps 被直接构造且留了零值」
// ——心跳间隔为 0 会让 time.NewTicker panic，而它跑在 goroutine 里，会连带
// 整个 hub 进程一起死。
const (
	defaultHandshakeTimeout  = 10 * time.Second
	defaultReadTimeout       = 70 * time.Second
	defaultHeartbeatInterval = 30 * time.Second
)

func NewHandler(d Deps) *Handler {
	if d.Clock == nil {
		d.Clock = clock.System()
	}
	if d.Registry == nil {
		d.Registry = machines.NopRegistry{}
	}
	if d.HandshakeTimeout <= 0 {
		d.HandshakeTimeout = defaultHandshakeTimeout
	}
	if d.ReadTimeout <= 0 {
		d.ReadTimeout = defaultReadTimeout
	}
	if d.HeartbeatInterval <= 0 {
		d.HeartbeatInterval = defaultHeartbeatInterval
	}
	h := &Handler{d: d, live: make(map[*gws.Conn]struct{})}
	h.log = slog.Default()
	if d.App != nil {
		h.log = d.App.Logger()
	}
	h.upgrader = gws.NewUpgrader(h, &gws.ServerOption{
		// 与 agent 侧同源，见 protocol.MaxPayload。
		ReadMaxPayloadSize: protocol.MaxPayload,
	})
	return h
}

// Upgrade 是挂到 PocketBase 路由上的入口。
func (h *Handler) Upgrade(e *core.RequestEvent) error {
	return h.UpgradeHTTP(e.Response, e.Request)
}

// UpgradeHTTP 是不依赖 PocketBase 的升级入口，供包内测试使用。
func (h *Handler) UpgradeHTTP(w http.ResponseWriter, r *http.Request) error {
	socket, err := h.upgrader.Upgrade(w, r)
	if err != nil {
		return err
	}
	// wg 必须在起 goroutine 之前 Add，否则 Wait 可能在读循环登记之前就返回。
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		socket.ReadLoop() // 阻塞直到连接结束
	}()
	return nil
}

// CloseAll 关闭全部活跃连接。读循环随后自行退出，由 Wait 收尾。
func (h *Handler) CloseAll() {
	h.mu.Lock()
	sockets := make([]*gws.Conn, 0, len(h.live))
	for s := range h.live {
		sockets = append(sockets, s)
	}
	h.mu.Unlock()

	for _, s := range sockets {
		_ = s.WriteClose(1001, []byte("hub shutting down"))
	}
}

// Wait 阻塞到所有读循环退出。
//
// 这是关停时的必要一步：读循环会写库（machines.Manager），若在数据库拆掉
// 之后才跑完，PocketBase 内部会对着 nil 的 DB 解引用。
func (h *Handler) Wait() { h.wg.Wait() }

// —— gws.Event 实现 ——

func (h *Handler) OnOpen(socket *gws.Conn) {
	c := &Conn{
		socket:        socket,
		session:       h.d.Handshake.NewSession(),
		stopHeartbeat: make(chan struct{}),
	}
	socket.Session().Store(sessionKey, c)

	h.mu.Lock()
	h.live[socket] = struct{}{}
	h.mu.Unlock()

	// 握手期用短 deadline：升级成功却迟迟不认证的连接必须被清掉。
	_ = socket.SetDeadline(time.Now().Add(h.d.HandshakeTimeout))
}

func (h *Handler) OnClose(socket *gws.Conn, err error) {
	h.mu.Lock()
	delete(h.live, socket)
	h.mu.Unlock()

	c, ok := h.connOf(socket)
	if !ok {
		return
	}
	c.stopHeartbeatLoop()
	if c.authenticated() {
		h.d.Registry.Unregister(c)
	}
	h.log.Debug("WS 连接关闭", "fingerprint", c.Fingerprint(), "error", err)
}

func (h *Handler) OnPing(socket *gws.Conn, payload []byte) {
	h.touch(socket)
	_ = socket.WritePong(payload)
}

func (h *Handler) OnPong(socket *gws.Conn, _ []byte) {
	// 任何收到的帧都重置 deadline —— 这是「hub 还活着」的唯一证据。
	h.touch(socket)
}

func (h *Handler) OnMessage(socket *gws.Conn, msg *gws.Message) {
	defer msg.Close()

	c, ok := h.connOf(socket)
	if !ok {
		return
	}

	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		// 解不开的帧说明对面不是我们的 agent，多说无益。
		h.log.Warn("收到无法解码的帧，断开连接", "remote", socket.RemoteAddr().String(), "error", err)
		_ = socket.WriteClose(1002, []byte("bad frame"))
		return
	}

	if !env.Kind.IsKnown() {
		// 新版 hub/agent 之间的向前兼容靠这一条（spec §3.2）。
		h.log.Warn("忽略未知消息类型", "kind", uint8(env.Kind), "fingerprint", c.Fingerprint())
		return
	}

	if !c.authenticated() {
		h.handleHandshake(socket, c, env)
		return
	}

	h.touch(socket)
	h.handleAuthenticated(c, env)
}

func (h *Handler) handleHandshake(socket *gws.Conn, c *Conn, env protocol.Envelope) {
	reply, outcome, err := c.session.Handle(env)
	if err != nil {
		h.log.Warn("握手协议违规，断开连接", "remote", socket.RemoteAddr().String(), "error", err)
		_ = socket.WriteClose(1002, []byte("handshake violation"))
		return
	}
	if len(reply) > 0 {
		if err := socket.WriteMessage(gws.OpcodeBinary, reply); err != nil {
			h.log.Warn("发送握手响应失败", "error", err)
			return
		}
	}
	if outcome == nil {
		return // 握手还在进行
	}

	if !outcome.OK {
		h.log.Warn("拒绝 agent 连接",
			"fingerprint", outcome.Fingerprint, "code", outcome.Code, "reason", outcome.Reason)
		if h.d.Events != nil && outcome.Code == protocol.CodeBadSignature {
			if err := h.d.Events.Write(events.KindAuthFailed, "", map[string]any{
				"fingerprint": outcome.Fingerprint,
				"reason":      outcome.Reason,
				"code":        outcome.Code,
			}); err != nil {
				h.log.Warn("写 auth.failed 事件失败", "error", err)
			}
		}
		_ = socket.WriteClose(1008, []byte(outcome.Reason))
		return
	}

	c.markAuthed(outcome.Fingerprint, outcome.MachineID)
	if err := h.d.Registry.Register(c); err != nil {
		h.log.Error("登记连接失败", "fingerprint", outcome.Fingerprint, "error", err)
		_ = socket.WriteClose(1011, []byte("registry error"))
		return
	}

	// 认证后换成长 deadline，并开始主动 ping。
	h.touch(socket)
	go h.heartbeat(socket, c)
	h.log.Info("agent 已连接", "fingerprint", outcome.Fingerprint, "machine", outcome.MachineID)
}

func (h *Handler) handleAuthenticated(c *Conn, env protocol.Envelope) {
	switch env.Kind {
	case protocol.KindMachineInfo:
		info, err := protocol.DecodePayload[protocol.MachineInfo](env)
		if err != nil {
			h.log.Warn("解析 MachineInfo 失败", "fingerprint", c.Fingerprint(), "error", err)
			return
		}
		if err := h.d.Registry.UpdateInfo(c, info); err != nil {
			h.log.Warn("更新机器信息失败", "fingerprint", c.Fingerprint(), "error", err)
		}
	default:
		// 已认证连接上收到握手类消息属于异常，但不值得断开。
		h.log.Warn("已认证连接收到意外消息", "kind", env.Kind.String(), "fingerprint", c.Fingerprint())
	}
}

// heartbeat 按配置的间隔主动 ping。心跳不走信封——直接用 WebSocket 原生帧，
// 省一次序列化，且 gws 已内置帧级 deadline 管理（spec §6.2）。
func (h *Handler) heartbeat(socket *gws.Conn, c *Conn) {
	ticker := h.d.Clock.NewTicker(h.d.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopHeartbeat:
			return
		case <-ticker.C():
			if err := socket.WritePing(nil); err != nil {
				return
			}
		}
	}
}

func (h *Handler) touch(socket *gws.Conn) {
	_ = socket.SetDeadline(time.Now().Add(h.d.ReadTimeout))
}

func (h *Handler) connOf(socket *gws.Conn) (*Conn, bool) {
	v, ok := socket.Session().Load(sessionKey)
	if !ok {
		return nil, false
	}
	c, ok := v.(*Conn)
	return c, ok
}
