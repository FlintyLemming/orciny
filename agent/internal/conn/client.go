package conn

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// State 是重连状态机的状态（spec §7.2）。
type State int

const (
	StateDisconnected State = iota
	StateHandshaking
	StateConnected
	// StateCompromised 是终态：hub 签名验证失败，永不重试。
	StateCompromised
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateHandshaking:
		return "handshaking"
	case StateConnected:
		return "connected"
	case StateCompromised:
		return "compromised"
	default:
		return "unknown"
	}
}

// ErrMachineRemoved 表示 hub 说这台机器已被删除，需要人工重新 enroll。
var ErrMachineRemoved = errors.New("conn: 机器已在面板中被删除，需重新 enroll")

type ClientConfig struct {
	// Dial 建立一条已完成握手的连接。抽成函数是为了让重连的全部时序
	// 分支都能在没有网络的条件下测试。
	Dial func(ctx context.Context) (*Session, error)

	Clock   clock.Clock
	Backoff *Backoff

	// RejectRetryInterval 是「被明确拒绝」时的固定重试间隔。0 → 5 分钟。
	RejectRetryInterval time.Duration

	Logger  *slog.Logger
	OnState func(State, error)
}

// Client 是 agent 的重连状态机。
type Client struct {
	cfg ClientConfig

	mu    sync.RWMutex
	state State
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.Clock == nil {
		cfg.Clock = clock.System()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.RejectRetryInterval == 0 {
		cfg.RejectRetryInterval = 5 * time.Minute
	}
	if cfg.Backoff == nil {
		cfg.Backoff = NewBackoff(time.Second, time.Minute, 0.2, nil)
	}
	return &Client{cfg: cfg}
}

func (c *Client) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Client) setState(s State, err error) {
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
	if c.cfg.OnState != nil {
		c.cfg.OnState(s, err)
	}
}

// Run 一直连着 hub，直到 ctx 取消或遇到终局错误。
//
// 终局错误只有两种：hub 签名验证失败（Compromised）与机器已被删除。
// 其余一切——网络不通、hub 没起来、指纹没登记、版本过旧——都会继续重试，
// 因为它们都可能被运维在另一头修好。
func (c *Client) Run(ctx context.Context) error {
	for {
		c.setState(StateHandshaking, nil)
		session, err := c.cfg.Dial(ctx)
		if err != nil {
			wait, fatal := c.classify(err)
			if fatal != nil {
				return fatal
			}
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}

		c.cfg.Backoff.Reset()
		c.setState(StateConnected, nil)
		c.cfg.Logger.Info("已连接 hub")

		select {
		case <-session.Done():
			err := session.Err()

			// 连接期的「授权已撤销」通知与握手期的拒绝必须走同一条分流
			// （spec §3.3：不区分握手期还是连接期）。少了这一步，机器在面板上
			// 被删之后 agent 只看到一次普通断开，会一直重连下去。
			var rej *RejectedError
			if errors.As(err, &rej) {
				wait, fatal := c.classify(err)
				if fatal != nil {
					return fatal
				}
				if err := c.sleep(ctx, wait); err != nil {
					return err
				}
				continue
			}

			c.setState(StateDisconnected, err)
			c.cfg.Logger.Warn("与 hub 的连接已断开", "error", err)
			if err := c.sleep(ctx, c.cfg.Backoff.Next()); err != nil {
				return err
			}
		case <-ctx.Done():
			_ = session.Close()
			c.setState(StateDisconnected, ctx.Err())
			return ctx.Err()
		}
	}
}

// classify 决定这次失败该等多久，或者干脆别等了。
// 返回的第二个值非 nil 表示终局错误，Run 应当直接返回它。
func (c *Client) classify(err error) (time.Duration, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, err
	}

	// hub 签名不匹配意味着要么在遭受中间人攻击，要么 hub 换了密钥
	// （恢复备份时丢了 orciny_hub_key.pem）。两种情况都需要人来判断，
	// agent 自作主张继续重试只会掩盖问题（spec §7.2）。
	if errors.Is(err, ErrHubSignature) {
		c.setState(StateCompromised, err)
		c.cfg.Logger.Error("hub 签名验证失败，停止一切重试。"+
			"请确认 hub 是否更换过密钥，或本机是否遭到中间人攻击",
			"error", err)
		return 0, err
	}

	var rej *RejectedError
	if errors.As(err, &rej) {
		if rej.Code == protocol.CodeMachineRemoved {
			c.setState(StateDisconnected, err)
			c.cfg.Logger.Error("hub 报告本机已被删除，停止重试；如需重新接入请执行 orciny-agent enroll",
				"reason", rej.Reason)
			return 0, ErrMachineRemoved
		}
		// 指纹未登记、签名错误、版本过旧：都可能被运维修好，
		// 用固定的长间隔重试，并且每次都记日志（spec §7.2）。
		c.setState(StateDisconnected, err)
		c.cfg.Logger.Warn("hub 拒绝连接，将按固定间隔重试",
			"code", rej.Code, "reason", rej.Reason, "interval", c.cfg.RejectRetryInterval)
		return c.cfg.RejectRetryInterval, nil
	}

	c.setState(StateDisconnected, err)
	wait := c.cfg.Backoff.Next()
	c.cfg.Logger.Warn("连接 hub 失败，稍后重试", "error", err, "retry_in", wait)
	return wait, nil
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := c.cfg.Clock.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
