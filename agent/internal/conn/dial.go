// Package conn 是 agent 侧的 WebSocket 客户端与握手。
// 重连状态机（退避、按原因码分流）在计划 7 加入 reconnect.go。
package conn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"

	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

// ErrHubSignature 表示 hub 的签名验证失败。
//
// 这是 agent 唯一「永不重试」的错误：要么正在遭中间人攻击，要么 hub 换了
// 密钥（恢复备份时丢了 orciny_hub_key.pem）。两种情况都需要人来判断，
// 自作主张重试只会掩盖问题（spec §7.2）。
var ErrHubSignature = errors.New("conn: hub 签名验证失败")

// RejectedError 是 hub 明确拒绝时的错误，携带原因码供重试策略分流。
type RejectedError struct {
	Code   uint8
	Reason string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("conn: hub 拒绝连接（code=%d）: %s", e.Code, e.Reason)
}

type Config struct {
	HubURL       string
	Identity     *identity.Identity
	HubPub       ed25519.PublicKey
	AgentVersion string
	Info         protocol.MachineInfo

	HandshakeTimeout time.Duration
	ReadTimeout      time.Duration

	Rand   io.Reader
	Logger *slog.Logger
}

// Session 是一条已完成握手的连接。
type Session struct {
	socket *gws.Conn
	done   chan struct{}

	once sync.Once
	mu   sync.Mutex
	err  error
}

func (s *Session) Done() <-chan struct{} { return s.done }

// Err 返回连接结束的原因；仍在连接中时返回 nil。
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// finish 记录连接结束的原因并唤醒 Done()。只有第一次调用生效——
// 重连循环靠 Done() 判断「这条连接完了」，原因以最先到达的那个为准。
func (s *Session) finish(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

// Close 主动关闭连接。没有 socket 的会话（测试脚手架造的）直接结束。
func (s *Session) Close() error {
	if s.socket == nil {
		s.finish(nil)
		return nil
	}
	return s.socket.WriteClose(1000, nil)
}

// Send 在已建立的会话上发一条信封（计划 7 的心跳外消息用）。
func (s *Session) Send(kind protocol.Kind, payload any) error {
	b, err := protocol.Encode(kind, nil, payload)
	if err != nil {
		return err
	}
	return s.socket.WriteMessage(gws.OpcodeBinary, b)
}

type handler struct {
	gws.BuiltinEventHandler
	inbox       chan protocol.Envelope
	done        chan struct{}
	closeOnce   sync.Once
	readTimeout time.Duration
	log         *slog.Logger
	session     *Session

	// connected 在握手完成后置真，用来分开两个阶段的收信规则：
	// 握手期的帧要交给 await，连接期的帧由 onConnected 直接处理。
	// 由读循环那个 goroutine 读、由 Dial 所在的 goroutine 写，故用 atomic。
	connected atomic.Bool
}

func (h *handler) OnMessage(socket *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	_ = socket.SetDeadline(time.Now().Add(h.readTimeout))

	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		h.log.Warn("收到无法解码的帧，忽略", "error", err)
		return
	}
	if !env.Kind.IsKnown() {
		// 新版 hub 给旧版 agent 发新消息时，忽略而不是崩溃（spec §3.2）。
		h.log.Warn("忽略未知消息类型", "kind", uint8(env.Kind))
		return
	}
	if h.connected.Load() {
		h.onConnected(socket, env)
		return
	}
	select {
	case h.inbox <- env:
	default:
		h.log.Warn("入站队列已满，丢弃消息", "kind", env.Kind.String())
	}
}

// onConnected 处理握手完成之后收到的帧。
//
// M0 里 hub 在连接期只会发一种业务帧：`AuthResult{OK:false}`，即 spec §3.3 的
// 「授权已撤销」通知（唯一触发场景是机器在面板上被删，§6.4c）。把它记成连接
// 结束的原因，重连循环就能像处理握手期的拒绝一样按 `Code` 分流——spec §3.3
// 要的正是「agent 侧只有一条处理路径」。
func (h *handler) onConnected(socket *gws.Conn, env protocol.Envelope) {
	if env.Kind != protocol.KindAuthResult {
		h.log.Warn("忽略连接期收到的意外消息", "kind", env.Kind.String())
		return
	}
	err := rejectionFrom(env)
	if err == nil {
		// 连接期的 OK:true 没有语义，忽略即可——但不能因此断开。
		return
	}
	h.log.Warn("hub 撤销了本连接的授权", "error", err)
	// 先记原因再关连接：finish 只认第一次调用，随后 OnClose 带来的
	// gws.CloseError 不会盖掉这个更具体的原因。
	if h.session != nil {
		h.session.finish(err)
	}
	_ = socket.WriteClose(1000, nil)
}

func (h *handler) OnPing(socket *gws.Conn, payload []byte) {
	_ = socket.SetDeadline(time.Now().Add(h.readTimeout))
	_ = socket.WritePong(payload)
}

func (h *handler) OnPong(socket *gws.Conn, _ []byte) {
	// agent 侧同样设 deadline，用于识别「hub 已消失但 TCP 未断」
	// （NAT 超时、中间设备静默丢弃）——到期即主动断开重连（spec §6.2）。
	_ = socket.SetDeadline(time.Now().Add(h.readTimeout))
}

func (h *handler) OnClose(_ *gws.Conn, err error) {
	if h.session != nil {
		h.session.finish(err)
		return
	}
	// 理论上到不了这里（session 在 ReadLoop 之前就挂好了），
	// 但 await 靠这个通道解除阻塞，宁可多一条兜底。
	h.closeOnce.Do(func() { close(h.done) })
}

// Dial 连接 hub 并完成握手，成功后立即上报 MachineInfo。
func Dial(ctx context.Context, cfg Config) (*Session, error) {
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	h := &handler{
		inbox:       make(chan protocol.Envelope, 8),
		done:        make(chan struct{}),
		readTimeout: cfg.ReadTimeout,
		log:         cfg.Logger,
	}

	socket, _, err := gws.NewClient(h, &gws.ClientOption{
		Addr:             wsURL(cfg.HubURL),
		HandshakeTimeout: cfg.HandshakeTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("conn: 连接 hub: %w", err)
	}

	// session 必须在 ReadLoop 启动之前挂上去：OnClose 会读 h.session，
	// 而它跑在读循环那个 goroutine 上。
	session := &Session{socket: socket, done: h.done}
	h.session = session
	go socket.ReadLoop()

	if err := handshakeWith(ctx, cfg, socket, h); err != nil {
		_ = socket.WriteClose(1000, nil)
		return nil, err
	}

	// 从这一刻起收到的帧按连接期规则走。必须排在 MachineInfo 之前置位：
	// hub 在发出 AuthResult{OK:true} 之后就登记了连接，撤销通知随时可能到来，
	// 晚一步置位就会让那条通知落进 inbox 再没人读。
	h.connected.Store(true)
	// 置位前若已有帧进了 inbox（窗口极窄但存在），补一次连接期处理。
	for {
		select {
		case env := <-h.inbox:
			h.onConnected(socket, env)
			continue
		default:
		}
		break
	}

	// 握手后的首条业务消息（spec §4.2 第 6 步）
	if err := session.Send(protocol.KindMachineInfo, cfg.Info); err != nil {
		_ = socket.WriteClose(1000, nil)
		return nil, fmt.Errorf("conn: 上报机器信息: %w", err)
	}

	_ = socket.SetDeadline(time.Now().Add(cfg.ReadTimeout))
	return session, nil
}

func handshakeWith(ctx context.Context, cfg Config, socket *gws.Conn, h *handler) error {
	deadline := time.Now().Add(cfg.HandshakeTimeout)
	_ = socket.SetDeadline(deadline)

	clientNonce, err := protocol.NewNonce(cfg.Rand)
	if err != nil {
		return fmt.Errorf("conn: 生成 clientNonce: %w", err)
	}

	hello, err := protocol.Encode(protocol.KindHello, nil, protocol.Hello{
		AgentVersion: cfg.AgentVersion,
		Fingerprint:  cfg.Identity.Fingerprint(),
		ClientNonce:  clientNonce,
	})
	if err != nil {
		return err
	}
	if err := socket.WriteMessage(gws.OpcodeBinary, hello); err != nil {
		return fmt.Errorf("conn: 发送 hello: %w", err)
	}

	env, err := h.await(ctx, deadline)
	if err != nil {
		return err
	}

	// hub 可能直接以 AuthResult 拒绝（版本过旧、指纹未登记）
	if env.Kind == protocol.KindAuthResult {
		return rejectionFrom(env)
	}
	if env.Kind != protocol.KindChallenge {
		return fmt.Errorf("conn: 期望 challenge，收到 %s", env.Kind)
	}
	ch, err := protocol.DecodePayload[protocol.Challenge](env)
	if err != nil {
		return err
	}

	// 验证 hub 身份 —— 这一步失败是终局的。
	if !ed25519.Verify(cfg.HubPub, protocol.HubSigPayload(clientNonce, ch.ServerNonce), ch.HubSig) {
		return ErrHubSignature
	}

	auth, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{
		AgentSig: cfg.Identity.Sign(protocol.AgentSigPayload(ch.ServerNonce, clientNonce)),
	})
	if err != nil {
		return err
	}
	if err := socket.WriteMessage(gws.OpcodeBinary, auth); err != nil {
		return fmt.Errorf("conn: 发送 auth: %w", err)
	}

	env, err = h.await(ctx, deadline)
	if err != nil {
		return err
	}
	if env.Kind != protocol.KindAuthResult {
		return fmt.Errorf("conn: 期望 auth_result，收到 %s", env.Kind)
	}
	return rejectionFrom(env)
}

// rejectionFrom 把 AuthResult 转成错误；OK 为真时返回 nil。
func rejectionFrom(env protocol.Envelope) error {
	res, err := protocol.DecodePayload[protocol.AuthResult](env)
	if err != nil {
		return err
	}
	if res.OK {
		return nil
	}
	return &RejectedError{Code: res.Code, Reason: res.Reason}
}

func (h *handler) await(ctx context.Context, deadline time.Time) (protocol.Envelope, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case env := <-h.inbox:
		return env, nil
	case <-h.done:
		return protocol.Envelope{}, errors.New("conn: 握手期间连接被关闭")
	case <-timer.C:
		return protocol.Envelope{}, errors.New("conn: 等待 hub 响应超时")
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	}
}

// wsURL 把 http(s) 的 hub 地址换成 ws(s) 的端点地址。
func wsURL(hubURL string) string {
	u := strings.TrimRight(hubURL, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	if strings.HasSuffix(u, "/api/orciny/ws") {
		return u
	}
	return u + "/api/orciny/ws"
}
