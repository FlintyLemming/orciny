package render

import (
	"bytes"
	"sort"
	"strings"

	"github.com/FlintyLemming/orciny/protocol"
)

// MinVarLen 是变量参与还原的长度下限（spec §6.4）。
//
// 变量不是秘密，值常常很短（main、1）。把 "1" 替回 {{var.tier}} 会把文件里
// 每一个 1 都改掉，diff 变得完全不可读。宁可放弃还原并标记，让人来看。
const MinVarLen = 4

type RestoreResult struct {
	Content []byte
	// Partial 表示有变量未能还原。收编前 UI 强制人工复核这类条目——
	// diff 里会显示 main vs {{var.workspace}}，用户看得懂，且不泄密。
	Partial bool
	// Safe 表示全部已知凭据值都已替出。为 false 时调用方**不得上报内容**，
	// 只报路径并置 Truncated（spec §6.4）。
	Safe bool
}

// Restore 把磁盘上的真实值替回占位符。
func Restore(content []byte, creds, vars map[string]string) RestoreResult {
	return RestoreWithBase(content, nil, creds, vars)
}

// RestoreWithBase 在有基线内容时做更精确的变量判定。
//
// base 是渲染**前**的基线（占位符形态）。给了它就能算出「渲染时该变量应当
// 出现几次」，与当前内容里的次数一比，对不上就说明用户自己也写了一个同值
// 的字符串——此时替回去会把用户写的东西也变成占位符，因此标记 Partial
// 让人复核（spec §6.4）。base 为 nil 时退化：仍然替，但次数 ≠ 1 就标记。
func RestoreWithBase(content, base []byte, creds, vars map[string]string) RestoreResult {
	// 磁盘内容里字面的 {{ 必须先转义，否则往返律 render(restore(x)) == x
	// 会在含模板语法的 CLAUDE.md 上破掉（spec §6.1）。
	out := protocol.EscapeLiteral(string(content))

	res := RestoreResult{Safe: true}

	// 按值长度降序：短值是长值的子串时，先替短的会把长值切碎，
	// 剩下的碎片再也匹配不上，于是密钥的一部分留在了内容里。
	for _, r := range replacements(creds, protocol.RefCred) {
		if r.value == "" || !strings.Contains(out, r.value) {
			continue
		}
		out = strings.ReplaceAll(out, r.value, r.token)
	}

	for _, r := range replacements(vars, protocol.RefVar) {
		if r.value == "" || len(r.value) < MinVarLen {
			if r.value != "" && strings.Contains(out, r.value) {
				// 太短、放弃还原：不泄密（变量本就不是秘密），但要让人看一眼。
				res.Partial = true
			}
			continue
		}
		got := strings.Count(out, r.value)
		if got == 0 {
			continue
		}
		want := 1
		if base != nil {
			want = strings.Count(string(base), r.token)
		}
		if got != want {
			res.Partial = true
		}
		out = strings.ReplaceAll(out, r.value, r.token)
	}

	// 再扫一遍：任一已知凭据值仍能被搜到即判定还原失败（spec §6.4）。
	// 这是最后一道闸——上面的替换逻辑将来若被改坏，这里会拦住它。
	for _, v := range creds {
		if v != "" && strings.Contains(out, v) {
			res.Safe = false
			break
		}
	}

	res.Content = []byte(out)

	// 往返律是正确性支点（spec §6.1）：restore 的产物必须能被 render 回原内容。
	// 典型反例是值恰好落在 "{" 后面，替换后变成 "{{{cred.x}}"，Parse 会挂；
	// 或 "{{" + 值 经 EscapeLiteral 后再替换，占位符边界错位。
	// 对不上或解不动时丢弃内容（Safe=false），调用方只报路径。
	if res.Safe {
		back, err := Render(res.Content, restoreLookup(creds, vars))
		if err != nil || !bytes.Equal(back, content) {
			res.Safe = false
		}
	}
	return res
}

func restoreLookup(creds, vars map[string]string) func(protocol.Ref) (string, bool) {
	return func(r protocol.Ref) (string, bool) {
		switch r.Kind {
		case protocol.RefCred:
			v, ok := creds[r.Name]
			return v, ok
		case protocol.RefVar:
			v, ok := vars[r.Name]
			return v, ok
		default:
			return "", false
		}
	}
}

type replacement struct {
	value string
	token string
}

func replacements(m map[string]string, kind protocol.RefKind) []replacement {
	out := make([]replacement, 0, len(m))
	for name, v := range m {
		out = append(out, replacement{
			value: v,
			token: "{{" + protocol.Ref{Kind: kind, Name: name}.String() + "}}",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].value) != len(out[j].value) {
			return len(out[i].value) > len(out[j].value)
		}
		return out[i].value < out[j].value // 等长时按值排，保证确定性
	})
	return out
}
