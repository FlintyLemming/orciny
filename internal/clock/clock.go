// Package clock 把「现在几点」和「过 N 秒叫我」抽象成接口，
// 使超时、宽限、心跳、退避这些逻辑可以在测试中被瞬间推进（spec §12.3）。
//
// 本包中立于 hub 与 agent：它不属于任何一侧，两侧都可以 import，
// 因此不构成 spec §2.2 所禁止的 hub↔agent 直接依赖。
package clock

import "time"

// Clock 是取时间与创建定时器的唯一入口。
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
	NewTicker(d time.Duration) Ticker
}

// Timer 语义对齐 time.Timer。
type Timer interface {
	C() <-chan time.Time
	// Stop 返回 true 表示定时器在本次调用前尚未触发。
	Stop() bool
	// Reset 重新计时，返回值语义同 Stop。
	Reset(d time.Duration) bool
}

// Ticker 语义对齐 time.Ticker。
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type systemClock struct{}

// System 返回走真实时间的时钟。生产代码用它，测试代码一律不用。
func System() Clock { return systemClock{} }

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(d time.Duration) Timer {
	return &systemTimer{t: time.NewTimer(d)}
}

func (systemClock) NewTicker(d time.Duration) Ticker {
	return &systemTicker{t: time.NewTicker(d)}
}

type systemTimer struct{ t *time.Timer }

func (s *systemTimer) C() <-chan time.Time        { return s.t.C }
func (s *systemTimer) Stop() bool                 { return s.t.Stop() }
func (s *systemTimer) Reset(d time.Duration) bool { return s.t.Reset(d) }

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time { return s.t.C }
func (s *systemTicker) Stop()               { s.t.Stop() }
