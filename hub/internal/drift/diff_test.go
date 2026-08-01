package drift_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
)

func TestUnifiedDiffShowsChanges(t *testing.T) {
	got := drift.UnifiedDiff(".claude/CLAUDE.md",
		[]byte("第一行\n第二行\n第三行\n"),
		[]byte("第一行\n改过的第二行\n第三行\n"))

	require.Contains(t, got, "-第二行")
	require.Contains(t, got, "+改过的第二行")
	require.Contains(t, got, " 第一行", "要有上下文行")
	require.Contains(t, got, ".claude/CLAUDE.md")
}

func TestUnifiedDiffForAddedFile(t *testing.T) {
	got := drift.UnifiedDiff(".claude/skills/foo/SKILL.md", nil, []byte("全新内容\n"))
	require.Contains(t, got, "+全新内容")
	// unified diff 头里有 "---"，这里只断言没有内容删除行。
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			t.Fatalf("新增文件不该有删除行: %q", line)
		}
	}
}

func TestUnifiedDiffForDeletedFile(t *testing.T) {
	got := drift.UnifiedDiff(".claude/gone.md", []byte("要没了\n"), nil)
	require.Contains(t, got, "-要没了")
}

func TestUnifiedDiffOfIdenticalContentIsEmpty(t *testing.T) {
	require.Empty(t, drift.UnifiedDiff("a", []byte("一样\n"), []byte("一样\n")))
}

// 二进制内容不该被渲染成一堆乱码塞进库里。
func TestUnifiedDiffOfBinaryIsSummarized(t *testing.T) {
	bin := []byte{0x00, 0x01, 0x02, 0xff, 0xfe}
	got := drift.UnifiedDiff("a.bin", nil, bin)
	require.Contains(t, got, "二进制")
	require.NotContains(t, got, "\x00")
}

// diff 会进库并直接渲染到 UI，必须有上限。
func TestUnifiedDiffIsTruncated(t *testing.T) {
	var sb strings.Builder
	for i := range 200000 {
		sb.WriteString("第")
		sb.WriteString(string(rune('0' + i%10)))
		sb.WriteString("行\n")
	}
	got := drift.UnifiedDiff("big.md", nil, []byte(sb.String()))
	require.LessOrEqual(t, len(got), drift.MaxDiffBytes+200)
	require.Contains(t, got, "已截断")
}
