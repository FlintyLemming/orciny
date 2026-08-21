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

// authTokenKey 是 provider 里唯一走凭据档的键。
const authTokenKey = "auth_token"

type RestoreResult struct {
	Content []byte
	// Partial 表示有变量未能还原。收编前 UI 强制人工复核这类条目——
	// diff 里会显示 main vs {{var.workspace}}，用户看得懂，且不泄密。
	Partial bool
	// Safe 表示全部已知凭据值都已替出。为 false 时调用方**不得上报内容**，
	// 只报路径并置 Truncated（spec §6.4）。
	Safe bool
}

// Values 是还原时可用的全部值。
//
// provider 的六个值在这里被分派进已有的两档，不新增第三档（M1.5 spec §4.2）：
// auth_token 是秘密，进凭据档（必须还原，失败即 Safe=false）；
// base_url 与四个模型槽不是秘密，进变量档（尽力而为，受 MinVarLen 约束）。
type Values struct {
	Creds    map[string]string
	Vars     map[string]string
	Provider map[string]string
}

// Restore 把磁盘上的真实值替回占位符。
func Restore(content []byte, v Values) RestoreResult {
	return RestoreWithBase(content, nil, v)
}

// RestoreWithBase 在有基线内容时做更精确的变量判定。
//
// base 是渲染**前**的基线（占位符形态）。给了它就能算出「渲染时该变量应当
// 出现几次」，与当前内容里的次数一比，对不上就说明用户自己也写了一个同值
// 的字符串——此时替回去会把用户写的东西也变成占位符，因此标记 Partial
// 让人复核（spec §6.4）。base 为 nil 时退化：仍然替，但次数 ≠ 1 就标记。
func RestoreWithBase(content, base []byte, v Values) RestoreResult {
	// 磁盘内容里字面的 {{ 必须先转义，否则往返律 render(restore(x)) == x
	// 会在含模板语法的 CLAUDE.md 上破掉（spec §6.1）。
	out := protocol.EscapeLiteral(string(content))

	res := RestoreResult{Safe: true}

	// 按值长度降序：短值是长值的子串时，先替短的会把长值切碎，
	// 剩下的碎片再也匹配不上，于是密钥的一部分留在了内容里。
	for _, r := range v.secretBucket() {
		if r.value == "" || !strings.Contains(out, r.value) {
			continue
		}
		out = strings.ReplaceAll(out, r.value, r.token)
	}

	for _, r := range v.varBucket() {
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

	// 再扫一遍：任一已知秘密值仍能被搜到即判定还原失败（spec §6.4）。
	// 这是最后一道闸——上面的替换逻辑将来若被改坏，这里会拦住它。
	for _, val := range v.secretValues() {
		if val != "" && strings.Contains(out, val) {
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
		back, err := Render(res.Content, restoreLookup(v))
		if err != nil || !bytes.Equal(back, content) {
			res.Safe = false
		}
	}
	return res
}

// secretBucket 是必须还原的值：全部凭据，加上 provider.auth_token。
//
// auth_token 顶着 provider. 前缀，但它是秘密，不能因为前缀就当成普通值
// （M1.5 spec §3.2）。
func (v Values) secretBucket() []replacement {
	out := replacements(v.Creds, protocol.RefCred)
	if tok := v.Provider[authTokenKey]; tok != "" {
		out = append(out, replacement{
			value: tok,
			token: tokenOf(protocol.RefProvider, authTokenKey),
			rank:  rankOf(protocol.RefProvider, authTokenKey),
		})
	}
	return dedupeByValue(sortByValueLenDesc(out))
}

// varBucket 是尽力而为的值：全部变量，加上 provider 除 auth_token 外的五个。
func (v Values) varBucket() []replacement {
	rest := make(map[string]string, len(v.Provider))
	for k, val := range v.Provider {
		if k != authTokenKey {
			rest[k] = val
		}
	}
	out := append(replacements(v.Vars, protocol.RefVar),
		replacements(rest, protocol.RefProvider)...)
	return dedupeByValue(sortByValueLenDesc(out))
}

// secretValues 是「还原后不允许再被搜到」的值集合（spec §6.4 的最后一道闸）。
func (v Values) secretValues() []string {
	out := make([]string, 0, len(v.Creds)+1)
	for _, val := range v.Creds {
		out = append(out, val)
	}
	if tok := v.Provider[authTokenKey]; tok != "" {
		out = append(out, tok)
	}
	return out
}

func restoreLookup(v Values) func(protocol.Ref) (string, bool) {
	return func(r protocol.Ref) (string, bool) {
		switch r.Kind {
		case protocol.RefCred:
			s, ok := v.Creds[r.Name]
			return s, ok
		case protocol.RefVar:
			s, ok := v.Vars[r.Name]
			return s, ok
		case protocol.RefProvider:
			s, ok := v.Provider[r.Name]
			return s, ok
		default:
			return "", false
		}
	}
}

type replacement struct {
	value string
	token string
	// rank 是同值时的优先级，小者胜出。四个模型槽常常填同一个值，
	// 光靠 token 的字典序决定谁留下会选中 model_haiku——因为 '_' < '}'，
	// {{provider.model_haiku}} 排在 {{provider.model}} 前面。留下的应该是
	// ProviderKeys 里靠前的那个（主模型槽），那是 UI 与语义上的主槽。
	rank int
}

func tokenOf(kind protocol.RefKind, name string) string {
	return "{{" + protocol.Ref{Kind: kind, Name: name}.String() + "}}"
}

// rankOf 给同值替换项定序：凭据与变量恒为 0，provider 按 ProviderKeys 的
// 下标顺延。名字不在白名单里（不该发生）时排到最后。
func rankOf(kind protocol.RefKind, name string) int {
	if kind != protocol.RefProvider {
		return 0
	}
	for i, k := range protocol.ProviderKeys {
		if k == name {
			return 1 + i
		}
	}
	return 1 + len(protocol.ProviderKeys)
}

func replacements(m map[string]string, kind protocol.RefKind) []replacement {
	out := make([]replacement, 0, len(m))
	for name, v := range m {
		out = append(out, replacement{
			value: v,
			token: tokenOf(kind, name),
			rank:  rankOf(kind, name),
		})
	}
	return out
}

// sortByValueLenDesc 按值长度降序。
//
// 短值是长值的子串时，先替短的会把长值切碎，剩下的碎片再也匹配不上，
// 于是密钥的一部分留在了内容里。等长时按值、再按 rank、最后按 token 排——
// 四个模型槽常常是同一个值，只按值排会让它们的相对次序不确定，
// 渲染就不再是纯函数。
func sortByValueLenDesc(out []replacement) []replacement {
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].value) != len(out[j].value) {
			return len(out[i].value) > len(out[j].value)
		}
		if out[i].value != out[j].value {
			return out[i].value < out[j].value
		}
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].token < out[j].token
	})
	return out
}

// dedupeByValue 同值只留排序后的第一条。
//
// 四个模型槽常常填同一个值（M1.5 spec §2.3）：此时「把值替成 token」是有
// 歧义的，替完第一个之后后面三个已经找不到东西可替。留一条、让既有的
// 「出现次数与基线对不上就标 Partial」逻辑去提醒人复核，比在这里发明第二套
// 定位规则安全。
func dedupeByValue(in []replacement) []replacement {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, r := range in {
		if r.value != "" && seen[r.value] {
			continue
		}
		seen[r.value] = true
		out = append(out, r)
	}
	return out
}
