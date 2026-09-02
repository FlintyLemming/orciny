// Package overrides 是本机覆盖层：差异点的切分、合并与撞车检测
// （M1.8 spec §4）。
//
// 一条 machine_overrides 记录 = 一个差异点。JSON 按 sjson 键路径定位，
// 文本按整份 base/mine 全文加行级三方合并。合并挂在
// configsync.Snapshot 组装快照的途中，落在**占位符空间**——
// agent 侧一行不改（spec §4.4）。
package overrides

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// ErrNotJSONObject：只有两侧都是 JSON 对象才走 json_key 一路。
var ErrNotJSONObject = errors.New("overrides: 两侧必须都是 JSON 对象才能按键切分")

// Point 是一个差异点。BaseValue / MineValue 是**原始** JSON 片段，
// 空串表示该键在这一侧不存在（spec §4.1）。
type Point struct {
	Selector  string
	BaseValue string
	MineValue string
}

// IsJSONObject 判断内容是否是一个合法的 JSON 对象。
// kind 由它决定：两侧都是对象 → json_key，否则 → text。
func IsJSONObject(b []byte) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(b, &m) == nil && m != nil
}

// EscapeSeg 转义一个键段，使它能安全地拼进 gjson / sjson 的路径。
//
// 路径语法里 . 是分隔符、* ? 是通配符、\ 是转义符。settings.json 里
// 带点的键是真实存在的（hook matcher、MCP server 名），不转义会让
// 覆盖层写到错误的位置——静默写坏配置（spec §4.1）。
func EscapeSeg(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '.', '*', '?':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CanonJSON 返回规范化形式，**只用于比较**。
//
// 两个 revision 之间的同一个值可能因为重新缩进而原始字节不同、语义相同；
// 撞车检测若比原始字节会误报。Go 对 map[string]any 的键排序是稳定的，
// 因此 Marshal 的结果可直接用于相等判断（spec §4.1）。
func CanonJSON(raw []byte) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Split 递归下降比较两棵 JSON 树，产出差异点（spec §4.1）。
//
// 规则：两侧都是 object → 逐键下降；其余（数组、标量、类型不同）→ 叶子。
// 数组整体当叶子是刻意的：按下标定位的 selector 在数组增删时会指向
// 错误的元素，那是静默写坏用户配置的经典路子（spec §1.4）。
func Split(base, mine []byte) ([]Point, error) {
	b, okB := asObject(base)
	m, okM := asObject(mine)
	if !okB || !okM {
		return nil, ErrNotJSONObject
	}
	var out []Point
	splitObj("", b, m, &out)
	return out, nil
}

func asObject(raw []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

func splitObj(prefix string, base, mine map[string]json.RawMessage, out *[]Point) {
	seen := map[string]bool{}
	keys := make([]string, 0, len(base)+len(mine))
	for k := range base {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range mine {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys) // 稳定顺序，UI 与测试都靠它

	for _, k := range keys {
		sel := EscapeSeg(k)
		if prefix != "" {
			sel = prefix + "." + sel
		}
		bv, bok := base[k]
		mv, mok := mine[k]

		if bok && mok {
			bo, bIsObj := asObject(bv)
			mo, mIsObj := asObject(mv)
			if bIsObj && mIsObj {
				splitObj(sel, bo, mo, out)
				continue
			}
			if sameJSON(bv, mv) {
				continue
			}
		}

		*out = append(*out, Point{
			Selector:  sel,
			BaseValue: rawOf(bok, bv),
			MineValue: rawOf(mok, mv),
		})
	}
}

func rawOf(ok bool, raw json.RawMessage) string {
	if !ok {
		return "" // 空串 = 该键在这一侧不存在
	}
	return string(raw)
}

func sameJSON(a, b json.RawMessage) bool {
	ca, errA := CanonJSON(a)
	cb, errB := CanonJSON(b)
	if errA != nil || errB != nil {
		return string(a) == string(b)
	}
	return ca == cb
}
