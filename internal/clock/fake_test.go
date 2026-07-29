package clock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/clock"
)

var base = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func TestFakeNowAdvances(t *testing.T) {
	f := clock.NewFake(base)
	require.Equal(t, base, f.Now())
	f.Advance(90 * time.Second)
	require.Equal(t, base.Add(90*time.Second), f.Now())
}

func TestFakeTimerFiresOnlyAfterDeadline(t *testing.T) {
	f := clock.NewFake(base)
	tm := f.NewTimer(5 * time.Second)

	f.Advance(4 * time.Second)
	select {
	case <-tm.C():
		t.Fatal("未到期就触发了")
	default:
	}

	f.Advance(time.Second)
	select {
	case got := <-tm.C():
		require.Equal(t, base.Add(5*time.Second), got, "触发时间应为逻辑到期时刻")
	default:
		t.Fatal("到期后未触发")
	}
}

func TestFakeTimerStopPreventsFire(t *testing.T) {
	f := clock.NewFake(base)
	tm := f.NewTimer(5 * time.Second)
	require.True(t, tm.Stop())
	require.False(t, tm.Stop(), "重复 Stop 返回 false")

	f.Advance(time.Minute)
	select {
	case <-tm.C():
		t.Fatal("Stop 之后仍然触发")
	default:
	}
	require.Equal(t, 0, f.TimerCount())
}

func TestFakeTimerResetRearms(t *testing.T) {
	f := clock.NewFake(base)
	tm := f.NewTimer(5 * time.Second)
	f.Advance(4 * time.Second)
	require.True(t, tm.Reset(5*time.Second), "未触发的定时器 Reset 返回 true")

	f.Advance(4 * time.Second) // 距新的到期点还差 1s
	select {
	case <-tm.C():
		t.Fatal("Reset 后按旧到期点触发了")
	default:
	}

	f.Advance(time.Second)
	<-tm.C()
}

func TestFakeTickerRepeats(t *testing.T) {
	f := clock.NewFake(base)
	tk := f.NewTicker(30 * time.Second)
	defer tk.Stop()

	f.Advance(90 * time.Second)
	// 通道容量为 1：一次 Advance 跨过多个周期只保留最后一次滴答，
	// 语义与 time.Ticker 一致（消费者慢则丢滴答）。
	select {
	case <-tk.C():
	default:
		t.Fatal("ticker 未触发")
	}
	require.Equal(t, 1, f.TimerCount(), "ticker 停止前一直在册")
}

func TestSystemClockNowIsMonotonic(t *testing.T) {
	c := clock.System()
	a := c.Now()
	b := c.Now()
	require.False(t, b.Before(a))
}
