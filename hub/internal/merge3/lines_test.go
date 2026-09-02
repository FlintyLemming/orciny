package merge3_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
)

func TestSplitJoinRoundTrip(t *testing.T) {
	for _, in := range []string{
		"",
		"a\n",
		"a\nb\n",
		"a\nb",     // 无尾换行
		"\n",       // 单个空行
		"a\n\nb\n", // 中间空行
	} {
		got := merge3.Join(merge3.SplitLines([]byte(in)))
		require.Equal(t, in, string(got), "输入 %q 必须逐字节还原", in)
	}
}

func TestSplitLinesKeepsNewline(t *testing.T) {
	require.Equal(t, []string{"a\n", "b"}, merge3.SplitLines([]byte("a\nb")))
	require.Equal(t, []string{"a\n", "b\n"}, merge3.SplitLines([]byte("a\nb\n")))
	require.Nil(t, merge3.SplitLines(nil))
}
