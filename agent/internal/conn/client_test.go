package conn_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

var epoch = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// dialRecorder 按脚本返回拨号结果，并记录被拨了几次。
type dialRecorder struct {
	mu      sync.Mutex
	results []func() (*conn.Session, error)
	calls   int
}

func (d *dialRecorder) dial(context.Context) (*conn.Session, error) {
	d.mu.Lock()
	i := d.calls
	d.calls++
	var fn func() (*conn.Session, error)
	if i < len(d.results) {
		fn = d.results[i]
	} else {
		fn = d.results[len(d.results)-1]
	}
	d.mu.Unlock()
	return fn()
}

func (d *dialRecorder) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func failWith(err error) func() (*conn.Session, error) {
	return func() (*conn.Session, error) { return nil, err }
}

// runClient 起 Run 并返回一个取结果的函数。
func runClient(t *testing.T, c *conn.Client) (context.CancelFunc, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, func() error {
		select {
		case err := <-errCh:
			return err
		case <-time.After(3 * time.Second):
			t.Fatal("Run 未在预期时间内返回")
			return nil
		}
	}
}

// waitAndFire 等重试定时器挂上，然后精确地把逻辑时间推过 want。
//
// 为什么不是「一次推进一大段、事后比对各次拨号时刻的间隔」：clock.Fake.Advance
// 触发到期定时器之后会把 now 一路推到目标时刻，于是重拨时读到的 Now() 恒等于
// 推进后的时刻，间隔恒等于推进量——那样的断言恒成立，证明不了等待时长。
// 这里改成逐次精确推进：先推到差 1ms，此刻定时器必须还在册（否则等待比预期短）；
// 再推完那 1ms，重拨必须发生（否则等待比预期长）。
func waitAndFire(t *testing.T, clk *clock.Fake, d *dialRecorder, want time.Duration) {
	t.Helper()

	// 定时器挂上，说明上一次拨号已经记完账，此刻的计数才是稳定的基准。
	require.Eventually(t, func() bool { return clk.TimerCount() == 1 },
		2*time.Second, 5*time.Millisecond, "重试定时器未挂上")
	before := d.count()

	clk.Advance(want - time.Millisecond)
	require.Equal(t, 1, clk.TimerCount(), "定时器提前到期，等待比 %s 短", want)
	require.Equal(t, before, d.count(), "等待未满就重拨了")

	clk.Advance(time.Millisecond)
	require.Eventually(t, func() bool { return d.count() == before+1 },
		2*time.Second, 5*time.Millisecond, "推过 %s 之后没有重拨，等待比预期长", want)
}

// 网络错误 → 指数退避序列（注入假时钟与固定随机源）
func TestNetworkErrorsBackOffExponentially(t *testing.T) {
	clk := clock.NewFake(epoch)
	d := &dialRecorder{results: []func() (*conn.Session, error){
		failWith(errors.New("connection refused")),
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial:    d.dial,
		Clock:   clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter),
	})
	cancel, wait := runClient(t, c)

	for _, want := range []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
	} {
		waitAndFire(t, clk, d, want)
	}
	require.Equal(t, 6, d.count())

	cancel()
	require.ErrorIs(t, wait(), context.Canceled)
}

// hub 明确拒绝（非致命码）→ 固定 5 分钟间隔，而不是指数退避
func TestRejectedCodesUseFixedInterval(t *testing.T) {
	for _, code := range []uint8{
		protocol.CodeUnknownFingerprint,
		protocol.CodeBadSignature,
		protocol.CodeVersionTooOld,
	} {
		t.Run(protocolCodeName(code), func(t *testing.T) {
			clk := clock.NewFake(epoch)
			d := &dialRecorder{results: []func() (*conn.Session, error){
				failWith(&conn.RejectedError{Code: code, Reason: "测试"}),
			}}

			c := conn.NewClient(conn.ClientConfig{
				Dial:                d.dial,
				Clock:               clk,
				Backoff:             conn.NewBackoff(time.Second, time.Minute, 0, nil),
				RejectRetryInterval: 5 * time.Minute,
			})
			cancel, wait := runClient(t, c)

			// 两次都是 5 分钟：被拒绝时不走指数退避。
			waitAndFire(t, clk, d, 5*time.Minute)
			waitAndFire(t, clk, d, 5*time.Minute)

			cancel()
			require.ErrorIs(t, wait(), context.Canceled)
		})
	}
}

// 机器被删除 → 停止重试
func TestMachineRemovedStopsRetrying(t *testing.T) {
	clk := clock.NewFake(epoch)
	d := &dialRecorder{results: []func() (*conn.Session, error){
		failWith(&conn.RejectedError{Code: protocol.CodeMachineRemoved, Reason: "已删除"}),
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0, nil),
	})
	_, wait := runClient(t, c)

	err := wait()
	require.ErrorIs(t, err, conn.ErrMachineRemoved)
	require.Equal(t, 1, d.count(), "不该再拨第二次")
	require.Equal(t, 0, clk.TimerCount(), "不该挂任何重试定时器")
}

// hub 签名验证失败 → Compromised 终态，永不重试
func TestHubSignatureFailureEntersCompromised(t *testing.T) {
	clk := clock.NewFake(epoch)
	d := &dialRecorder{results: []func() (*conn.Session, error){
		failWith(conn.ErrHubSignature),
	}}

	var states []conn.State
	var mu sync.Mutex
	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0, nil),
		OnState: func(s conn.State, _ error) {
			mu.Lock()
			states = append(states, s)
			mu.Unlock()
		},
	})
	_, wait := runClient(t, c)

	require.ErrorIs(t, wait(), conn.ErrHubSignature)
	require.Equal(t, conn.StateCompromised, c.State())
	require.Equal(t, 1, d.count())

	mu.Lock()
	require.Contains(t, states, conn.StateCompromised)
	mu.Unlock()
}

// 连接成功后退避重置：一次成功之后再失败，应当重新从 base 开始
func TestBackoffResetsAfterSuccessfulConnection(t *testing.T) {
	clk := clock.NewFake(epoch)
	fail := failWith(errors.New("boom"))

	sessions := make(chan *conn.Session, 1)
	ok := func() (*conn.Session, error) {
		s := conn.NewTestSession()
		sessions <- s
		return s, nil
	}

	d := &dialRecorder{results: []func() (*conn.Session, error){
		fail, fail, ok, fail, fail,
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter),
	})
	cancel, wait := runClient(t, c)

	// 两次失败：1s、2s
	waitAndFire(t, clk, d, time.Second)
	waitAndFire(t, clk, d, 2*time.Second)

	// 第三次成功，随后主动断开
	s := <-sessions
	require.Eventually(t, func() bool { return c.State() == conn.StateConnected },
		2*time.Second, 5*time.Millisecond)
	s.CloseForTest(errors.New("hub 走了"))

	// 断开后应从 base 重新开始，而不是接着 4s
	waitAndFire(t, clk, d, time.Second)

	cancel()
	require.ErrorIs(t, wait(), context.Canceled)
}

func TestStateTransitions(t *testing.T) {
	clk := clock.NewFake(epoch)
	sessions := make(chan *conn.Session, 1)
	d := &dialRecorder{results: []func() (*conn.Session, error){
		func() (*conn.Session, error) {
			s := conn.NewTestSession()
			sessions <- s
			return s, nil
		},
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0, nil),
	})
	require.Equal(t, conn.StateDisconnected, c.State())

	cancel, wait := runClient(t, c)
	<-sessions
	require.Eventually(t, func() bool { return c.State() == conn.StateConnected },
		2*time.Second, 5*time.Millisecond)

	cancel()
	require.ErrorIs(t, wait(), context.Canceled)
}

func TestStateString(t *testing.T) {
	require.Equal(t, "disconnected", conn.StateDisconnected.String())
	require.Equal(t, "handshaking", conn.StateHandshaking.String())
	require.Equal(t, "connected", conn.StateConnected.String())
	require.Equal(t, "compromised", conn.StateCompromised.String())
}

func protocolCodeName(code uint8) string {
	switch code {
	case protocol.CodeUnknownFingerprint:
		return "unknown_fingerprint"
	case protocol.CodeBadSignature:
		return "bad_signature"
	case protocol.CodeVersionTooOld:
		return "version_too_old"
	default:
		return "other"
	}
}
