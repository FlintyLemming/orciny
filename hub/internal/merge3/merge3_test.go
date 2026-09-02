package merge3_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
)

func lines(s string) []string { return merge3.SplitLines([]byte(s)) }
func text(ls []string) string { return string(merge3.Join(ls)) }

func TestMergeOnlyMineChanged(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nB\nc\n"), lines("a\nb\nc\n"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nB\nc\n", text(out))
}

func TestMergeOnlyTheirsChanged(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nb\nc\n"), lines("a\nb\nC\n"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nb\nC\n", text(out))
}

func TestMergeBothChangedSameWay(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nX\nc\n"), lines("a\nX\nc\n"))
	require.Empty(t, conflicts, "改成同样的内容不是冲突")
	require.Equal(t, "a\nX\nc\n", text(out))
}

func TestMergeBothChangedDifferentlyTakesMine(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nMINE\nc\n"), lines("a\nTHEIRS\nc\n"))
	require.Len(t, conflicts, 1)
	require.Equal(t, "a\nMINE\nc\n", text(out), "冲突一律取 mine")
	// 冲突区间用输出行坐标：第 1 行（0 起）那一行。
	require.Equal(t, merge3.Range{Start: 1, End: 2}, conflicts[0])
}

func TestMergeDisjointChangesBothApply(t *testing.T) {
	base := lines("1\n2\n3\n4\n5\n6\n7\n8\n9\n")
	mine := lines("MINE\n2\n3\n4\n5\n6\n7\n8\n9\n")
	theirs := lines("1\n2\n3\n4\n5\n6\n7\n8\nTHEIRS\n")
	out, conflicts := merge3.Merge(base, mine, theirs)
	require.Empty(t, conflicts, "隔得远的两处改动不该判冲突")
	require.Equal(t, "MINE\n2\n3\n4\n5\n6\n7\n8\nTHEIRS\n", text(out))
}

func TestMergeEmptyInputs(t *testing.T) {
	out, conflicts := merge3.Merge(nil, nil, nil)
	require.Empty(t, conflicts)
	require.Empty(t, out)

	out, conflicts = merge3.Merge(nil, lines("mine\n"), nil)
	require.Empty(t, conflicts)
	require.Equal(t, "mine\n", text(out))
}

func TestMergeNoTrailingNewline(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb"), lines("a\nB"), lines("a\nb"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nB", text(out), "不许凭空补尾换行")
}

func TestMergeDeletionOnOneSide(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nc\n"), lines("a\nb\nc\n"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nc\n", text(out))
}
