package conn_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/conn"
)

// noJitter 让随机数固定落在区间中点，抖动因子恰好为 1，
// 于是可以逐项断言退避序列本身。
func noJitter() float64 { return 0.5 }

func TestBackoffDoublesUpToMax(t *testing.T) {
	b := conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter)

	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
	}
	for i, w := range want {
		require.Equal(t, w, b.Next(), "第 %d 次退避", i+1)
	}
}

func TestBackoffResetGoesBackToBase(t *testing.T) {
	b := conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter)
	b.Next()
	b.Next()
	b.Next()

	b.Reset()
	require.Equal(t, time.Second, b.Next(), "连接成功后退避必须重置")
}

// 抖动不是可选项：机队规模上去后，hub 重启会让所有 agent 在同一秒重连，
// 抖动把这个尖峰摊平（spec §7.2）。
func TestJitterStaysWithinTwentyPercent(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	b := conn.NewBackoff(10*time.Second, time.Minute, 0.2, rnd.Float64)

	sawBelow, sawAbove := false, false
	for i := 0; i < 200; i++ {
		b.Reset()
		d := b.Next()
		require.GreaterOrEqual(t, d, 8*time.Second, "不得低于 base 的 80%%")
		require.LessOrEqual(t, d, 12*time.Second, "不得高于 base 的 120%%")
		if d < 10*time.Second {
			sawBelow = true
		}
		if d > 10*time.Second {
			sawAbove = true
		}
	}
	require.True(t, sawBelow && sawAbove, "抖动应当双向散开，而不是恒定偏移")
}

func TestJitterAppliesAtMaxToo(t *testing.T) {
	b := conn.NewBackoff(time.Second, 10*time.Second, 0.2, func() float64 { return 0 })
	for i := 0; i < 10; i++ {
		b.Next()
	}
	d := b.Next()
	require.Equal(t, 8*time.Second, d, "封顶后仍然抖动，否则封顶点会再次形成尖峰")
}

func TestZeroJitterIsDeterministic(t *testing.T) {
	b := conn.NewBackoff(time.Second, time.Minute, 0, func() float64 { return 0 })
	require.Equal(t, time.Second, b.Next())
	require.Equal(t, 2*time.Second, b.Next())
}
