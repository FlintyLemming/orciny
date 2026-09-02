package overrides_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/overrides"
)

const (
	synthBase = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nl16\n"
	synthCur  = "L1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nL16\n"
)

func TestHunksMatchUnifiedDiffHunkCount(t *testing.T) {
	// 前端勾的是 unified diff 的 @@ 块下标，后端按同一下标取 hunk。
	// 这条一一对应关系是「勾选」能落地的前提（spec §4.2）。
	hs := overrides.Hunks([]byte(synthBase), []byte(synthCur))
	d := drift.UnifiedDiff("f", []byte(synthBase), []byte(synthCur))
	require.Equal(t, strings.Count(d, "\n@@"), len(hs))
	require.Len(t, hs, 2, "首尾各一处改动、中间隔得够远，应是两个 hunk")
}

func TestSynthesizeMineKeepAllEqualsCur(t *testing.T) {
	hs := overrides.Hunks([]byte(synthBase), []byte(synthCur))
	all := make([]int, len(hs))
	for i := range hs {
		all[i] = i
	}
	got := overrides.SynthesizeMine([]byte(synthBase), []byte(synthCur), all)
	require.Equal(t, synthCur, string(got))
}

func TestSynthesizeMineKeepNoneEqualsBase(t *testing.T) {
	got := overrides.SynthesizeMine([]byte(synthBase), []byte(synthCur), nil)
	require.Equal(t, synthBase, string(got))
}

func TestSynthesizeMineDropsOneHunk(t *testing.T) {
	// 只留第一处改动，第二处退回基线。
	got := overrides.SynthesizeMine([]byte(synthBase), []byte(synthCur), []int{0})
	require.True(t, strings.HasPrefix(string(got), "L1\n"))
	require.True(t, strings.HasSuffix(string(got), "l16\n"))
}

func TestSynthesizeMineNoTrailingNewline(t *testing.T) {
	got := overrides.SynthesizeMine([]byte("a\nb"), []byte("a\nB"), []int{0})
	require.Equal(t, "a\nB", string(got), "不许凭空补尾换行")
}

func TestHunksOnIdenticalContent(t *testing.T) {
	require.Empty(t, overrides.Hunks([]byte("a\n"), []byte("a\n")))
}
