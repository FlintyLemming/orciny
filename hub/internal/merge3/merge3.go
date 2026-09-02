package merge3

import "github.com/pmezard/go-difflib/difflib"

// Range 是 out 里一段冲突的半开区间 [Start, End)。
type Range struct {
	Start int
	End   int
}

// hunk 是某一侧相对 base 的一段改动：把 base[start:end) 换成 repl。
type hunk struct {
	start, end int
	repl       []string
}

// hunksOf 求 base → other 的全部改动段（跳过相等段）。
func hunksOf(base, other []string) []hunk {
	m := difflib.NewMatcher(base, other)
	var out []hunk
	for _, op := range m.GetOpCodes() {
		if op.Tag == 'e' {
			continue
		}
		out = append(out, hunk{start: op.I1, end: op.I2, repl: other[op.J1:op.J2]})
	}
	return out
}

// applyIn 把一组 hunk 应用到 base[start:end) 上，得到该侧对这一段的说法。
func applyIn(base []string, hs []hunk, start, end int) []string {
	out := make([]string, 0, end-start)
	pos := start
	for _, h := range hs {
		if h.start > pos {
			out = append(out, base[pos:h.start]...)
		}
		out = append(out, h.repl...)
		pos = h.end
	}
	if pos < end {
		out = append(out, base[pos:end]...)
	}
	return out
}

func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Merge 做行级三方合并。冲突一律取 mine 侧，并在 conflicts 里记下区间。
//
// 沿 base 的行号推进，把两侧的改动切成对齐的区段：
//   - 只有一侧改 → 取改的那侧
//   - 两侧改成同样的内容 → 取其一
//   - 两侧改成不同内容 → 冲突，取 mine，记一条 Range
//
// 不生成 <<<<<<< 标记：产物直接落到用户的 ~/.claude 下被 Claude Code
// 读取，写进标记等于交付一个坏文件（spec §5）。
func Merge(base, mine, theirs []string) ([]string, []Range) {
	hm := hunksOf(base, mine)
	ht := hunksOf(base, theirs)

	var out []string
	var conflicts []Range
	i, a, b := 0, 0, 0

	for a < len(hm) || b < len(ht) {
		// 组的起点 = 两侧下一个 hunk 里靠前的那个。
		groupStart := len(base)
		if a < len(hm) && hm[a].start < groupStart {
			groupStart = hm[a].start
		}
		if b < len(ht) && ht[b].start < groupStart {
			groupStart = ht[b].start
		}
		if i < groupStart {
			out = append(out, base[i:groupStart]...)
			i = groupStart
		}

		// 把与本组真正重叠的 hunk 都收进来。
		// 判据是严格重叠（h.start < groupEnd），相邻但不重叠的两处改动
		// 不该被并成一组——否则「两侧在相邻行各改一处」会误判冲突。
		// h.start == groupStart 是纯插入（start == end）的特例，必须收进来。
		groupEnd := groupStart
		ma, mb := a, b
		for {
			grew := false
			for ma < len(hm) && (hm[ma].start < groupEnd || hm[ma].start == groupStart) {
				if hm[ma].end > groupEnd {
					groupEnd = hm[ma].end
				}
				ma++
				grew = true
			}
			for mb < len(ht) && (ht[mb].start < groupEnd || ht[mb].start == groupStart) {
				if ht[mb].end > groupEnd {
					groupEnd = ht[mb].end
				}
				mb++
				grew = true
			}
			if !grew {
				break
			}
		}

		mineVer := applyIn(base, hm[a:ma], groupStart, groupEnd)
		theirsVer := applyIn(base, ht[b:mb], groupStart, groupEnd)

		switch {
		case ma == a: // 只有 theirs 改了这一段
			out = append(out, theirsVer...)
		case mb == b: // 只有 mine 改了这一段
			out = append(out, mineVer...)
		case sameLines(mineVer, theirsVer):
			out = append(out, mineVer...)
		default:
			start := len(out)
			out = append(out, mineVer...)
			conflicts = append(conflicts, Range{Start: start, End: len(out)})
		}

		a, b, i = ma, mb, groupEnd
	}

	if i < len(base) {
		out = append(out, base[i:]...)
	}
	return out, conflicts
}
