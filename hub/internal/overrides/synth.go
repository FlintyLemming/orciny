package overrides

import (
	"github.com/pmezard/go-difflib/difflib"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
)

// Hunk 是 base → cur 的一段改动，含 3 行上下文。
// 下标与 drift.UnifiedDiff 里的 @@ 块一一对应（spec §4.2）——
// 前端勾的是 @@ 块下标，SynthesizeMine 按同一下标取舍。
type Hunk struct {
	BaseStart, BaseEnd int
	CurStart, CurEnd   int
}

// Hunks 用与 drift.UnifiedDiff 同一套分组（3 行上下文）切出 hunk。
//
// 每次新建 Matcher：difflib 的 GetGroupedOpCodes 会就地改写自己缓存的
// opcodes，复用同一个 Matcher 会拿到被改过的数据。
func Hunks(base, cur []byte) []Hunk {
	bl := merge3.SplitLines(base)
	cl := merge3.SplitLines(cur)
	m := difflib.NewMatcher(bl, cl)

	var out []Hunk
	for _, g := range m.GetGroupedOpCodes(3) {
		if len(g) == 0 {
			continue
		}
		out = append(out, Hunk{
			BaseStart: g[0].I1, BaseEnd: g[len(g)-1].I2,
			CurStart: g[0].J1, CurEnd: g[len(g)-1].J2,
		})
	}
	return out
}

// SynthesizeMine 合成「基线 + 被勾选的那些 hunk」（spec §4.2）。
//
// keep 是被勾选的 hunk 下标。keep 为全集时结果 == cur；keep 为空时
// 结果 == base。勾选因此塌缩进数据本身，后续所有逻辑只面对 base / mine
// 两份全文，不需要第二份真相。
func SynthesizeMine(base, cur []byte, keep []int) []byte {
	bl := merge3.SplitLines(base)
	cl := merge3.SplitLines(cur)
	hs := Hunks(base, cur)

	want := make(map[int]bool, len(keep))
	for _, i := range keep {
		want[i] = true
	}

	out := make([]string, 0, len(cl))
	pos := 0 // base 行号
	for i, h := range hs {
		if h.BaseStart > pos {
			out = append(out, bl[pos:h.BaseStart]...)
		}
		if want[i] {
			out = append(out, cl[h.CurStart:h.CurEnd]...)
		} else {
			out = append(out, bl[h.BaseStart:h.BaseEnd]...)
		}
		pos = h.BaseEnd
	}
	if pos < len(bl) {
		out = append(out, bl[pos:]...)
	}
	return merge3.Join(out)
}
