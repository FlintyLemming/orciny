package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake 是可手工推进的时钟。
//
// 用法：被测代码持有 Fake 并注册定时器，测试调用 Advance 推进逻辑时间，
// 然后用 require.Eventually 等待「推进所引发的可观测效果」——
// 不要用 time.Sleep 等待，那正是本包要消灭的东西。
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func NewFake(now time.Time) *Fake { return &Fake{now: now} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// TimerCount 返回在册（未触发且未停止）的定时器数量。
// 测试用它确认「被测代码已经注册好定时器」再 Advance，避免竞态。
func (f *Fake) TimerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// Advance 把逻辑时间推进 d，并按到期顺序触发所有到期的定时器。
// 触发时 f.now 已被推进到该定时器的到期时刻，因此定时器回调里读到的
// Now() 与到期时间一致。
func (f *Fake) Advance(d time.Duration) {
	target := f.Now().Add(d)

	for {
		f.mu.Lock()
		sort.SliceStable(f.timers, func(i, j int) bool {
			return f.timers[i].deadline.Before(f.timers[j].deadline)
		})
		if len(f.timers) == 0 || f.timers[0].deadline.After(target) {
			f.now = target
			f.mu.Unlock()
			return
		}
		t := f.timers[0]
		f.now = t.deadline
		fireAt := t.deadline
		if t.period > 0 {
			t.deadline = t.deadline.Add(t.period)
		} else {
			f.timers = f.timers[1:]
			t.armed = false
		}
		f.mu.Unlock()

		// 通道容量为 1，非阻塞发送：消费者未取走上一次滴答时直接丢弃，
		// 与 time.Ticker 行为一致，也保证 Advance 永不死锁。
		select {
		case t.ch <- fireAt:
		default:
		}
	}
}

func (f *Fake) NewTimer(d time.Duration) Timer   { return f.add(d, 0) }
func (f *Fake) NewTicker(d time.Duration) Ticker { return fakeTicker{f.add(d, d)} }

// fakeTicker 把 fakeTimer 适配成 Ticker：两者的到期逻辑完全相同，
// 只有 Stop 的签名不同（Ticker.Stop 不返回值）。
type fakeTicker struct{ *fakeTimer }

func (t fakeTicker) Stop() { t.fakeTimer.Stop() }

func (f *Fake) add(d, period time.Duration) *fakeTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{
		f:        f,
		deadline: f.now.Add(d),
		period:   period,
		ch:       make(chan time.Time, 1),
		armed:    true,
	}
	f.timers = append(f.timers, t)
	return t
}

type fakeTimer struct {
	f        *Fake
	deadline time.Time
	period   time.Duration
	ch       chan time.Time
	armed    bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	return t.removeLocked()
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	was := t.removeLocked()
	t.deadline = t.f.now.Add(d)
	t.armed = true
	t.f.timers = append(t.f.timers, t)
	return was
}

func (t *fakeTimer) removeLocked() bool {
	if !t.armed {
		return false
	}
	for i, x := range t.f.timers {
		if x == t {
			t.f.timers = append(t.f.timers[:i], t.f.timers[i+1:]...)
			break
		}
	}
	t.armed = false
	return true
}
