package conn

import "time"

// Backoff 是带抖动的指数退避（spec §7.2）。非并发安全——由重连循环独占。
type Backoff struct {
	base   time.Duration
	max    time.Duration
	jitter float64
	rnd    func() float64 // 返回 [0,1)，注入以便测试

	cur time.Duration
}

// NewBackoff 构造退避器。jitter 是比例，0.2 表示 ±20%。
// rnd 传 nil 时不抖动（仅测试用；生产必须传真随机源）。
func NewBackoff(base, max time.Duration, jitter float64, rnd func() float64) *Backoff {
	if rnd == nil {
		rnd = func() float64 { return 0.5 }
	}
	return &Backoff{base: base, max: max, jitter: jitter, rnd: rnd}
}

// Next 返回下一次等待时长并推进内部状态。
func (b *Backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.base
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	return b.applyJitter(b.cur)
}

// Reset 把退避拉回起点。连接成功后必须调用。
func (b *Backoff) Reset() { b.cur = 0 }

// applyJitter 把 d 乘以 [1-jitter, 1+jitter] 之间的一个因子。
// 封顶后同样抖动 —— 否则所有 agent 会在 60 秒这个点上重新对齐。
func (b *Backoff) applyJitter(d time.Duration) time.Duration {
	if b.jitter <= 0 {
		return d
	}
	factor := 1 + (b.rnd()*2-1)*b.jitter
	return time.Duration(float64(d) * factor)
}
