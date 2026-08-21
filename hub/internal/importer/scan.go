// Package importer 是导入向导的后端：采集编排与敏感项检测（spec §9）。
package importer

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
)

// Finding 是一条待人工处置的敏感项。三个动作由 UI 给出：
// 抽取为凭据 / 保留明文（二次确认）/ 把该文件移出纳管范围。
type Finding struct {
	Path      string `json:"path"`
	Location  string `json:"location"` // 结构化位置，如 env.ANTHROPIC_AUTH_TOKEN
	Key       string `json:"key"`
	Masked    string `json:"masked"`    // 前 4 后 4，中间省略
	Suggested string `json:"suggested"` // 由键名派生的建议凭据名
	Rule      string `json:"rule"`
}

// 命中规则的名字，UI 按它排优先级。
const (
	RuleStructured   = "structured"
	RuleKeyName      = "key_name"
	RuleValuePrefix  = "value_prefix"
	RuleValueEntropy = "value_entropy"
)

// 检测阈值（spec §9.2）。
const (
	minEntropyLen = 32
	minEntropy    = 3.5
)

var (
	keyNamePattern = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|credential|auth)`)
	// 已知前缀。全文兜底也用它——CLAUDE.md 里粘了一个 key 也要能抓到。
	valuePrefixes = []string{
		"sk-ant-", "sk-", "ghp_", "gho_", "github_pat_", "xoxb-", "AKIA", "AIza", "glpat-",
	}
	prefixPattern = regexp.MustCompile(
		`(sk-ant-|sk-|ghp_|gho_|github_pat_|xoxb-|AKIA|AIza|glpat-)[A-Za-z0-9\-_]{8,}`)
	opaquePattern = regexp.MustCompile(`^[A-Za-z0-9+/_=-]+$`)
	// 已被抽成占位符的值不再报——否则 Extract 之后 Findings 会把同一条再吐回来。
	// provider.* 同理：绑了服务的配置集里它到处都是（M1.5 spec §3.1）。
	placeholderValue = regexp.MustCompile(
		`^\{\{(cred|var|machine|provider)\.[A-Za-z0-9_-]+\}\}$`)
)

// Scan 对一份内容跑三层检测。
//
// 误报可接受，漏报不可接受——规则宁滥勿缺，交互上让用户一键跳过（spec §9.2）。
func Scan(path string, content []byte) []Finding {
	var out []Finding
	seen := map[string]bool{}
	add := func(f Finding) {
		if seen[f.Location] {
			return
		}
		seen[f.Location] = true
		out = append(out, f)
	}

	if gjson.ValidBytes(content) {
		scanStructured(path, content, add)
		scanJSONKeys(path, content, add)
	}
	scanFullText(path, content, add)
	return out
}

// scanStructured 扫两处结构化位置：settings.json 的 env、
// .claude.json 的 mcpServers.*.env。这两处准确率最高，UI 优先展示。
func scanStructured(path string, content []byte, add func(Finding)) {
	emit := func(prefix string, obj gjson.Result) {
		obj.ForEach(func(k, v gjson.Result) bool {
			val := v.String()
			// 已经是占位符的值不需要再抽。
			if placeholderValue.MatchString(val) {
				return true
			}
			// env 里的每一项都报——它们按定义就是要注入进进程环境的东西。
			add(Finding{
				Path: path, Location: prefix + k.String(), Key: k.String(),
				Masked: Mask(val), Suggested: SuggestName(k.String()),
				Rule: RuleStructured,
			})
			return true
		})
	}

	base := strings.TrimSuffix(path, "/")
	switch {
	case strings.HasSuffix(base, ".claude/settings.json"), base == "settings.json":
		emit("env.", gjson.GetBytes(content, "env"))
	case strings.HasSuffix(base, ".claude.json"), base == ".claude.json":
		gjson.GetBytes(content, "mcpServers").ForEach(func(name, srv gjson.Result) bool {
			emit("mcpServers."+name.String()+".env.", srv.Get("env"))
			return true
		})
	}
}

// scanJSONKeys 走键名与值特征，覆盖结构化位置之外的地方。
func scanJSONKeys(path string, content []byte, add func(Finding)) {
	var walk func(prefix string, r gjson.Result)
	walk = func(prefix string, r gjson.Result) {
		r.ForEach(func(k, v gjson.Result) bool {
			loc := k.String()
			if prefix != "" {
				loc = prefix + "." + k.String()
			}
			switch {
			case v.IsObject():
				walk(loc, v)
			case v.Type == gjson.String:
				val := v.String()
				if placeholderValue.MatchString(val) {
					return true
				}
				switch {
				case keyNamePattern.MatchString(k.String()):
					add(Finding{Path: path, Location: loc, Key: k.String(),
						Masked: Mask(val), Suggested: SuggestName(k.String()), Rule: RuleKeyName})
				case hasKnownPrefix(val):
					add(Finding{Path: path, Location: loc, Key: k.String(),
						Masked: Mask(val), Suggested: SuggestName(k.String()), Rule: RuleValuePrefix})
				case looksOpaque(val):
					add(Finding{Path: path, Location: loc, Key: k.String(),
						Masked: Mask(val), Suggested: SuggestName(k.String()), Rule: RuleValueEntropy})
				}
			}
			return true
		})
	}
	walk("", gjson.ParseBytes(content))
}

// scanFullText 是兜底：CLAUDE.md 里粘了一个 key 也要能抓到（spec §9.2）。
func scanFullText(path string, content []byte, add func(Finding)) {
	for i, m := range prefixPattern.FindAllString(string(content), -1) {
		add(Finding{
			Path:      path,
			Location:  fullTextLocation(i),
			Key:       "",
			Masked:    Mask(m),
			Suggested: SuggestName(path),
			Rule:      RuleValuePrefix,
		})
	}
}

func fullTextLocation(i int) string {
	if i == 0 {
		return "全文匹配"
	}
	return "全文匹配 #" + strconv.Itoa(i+1)
}

func hasKnownPrefix(v string) bool {
	for _, p := range valuePrefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

// looksOpaque：长度 ≥ 32、字符集像 base64/token、且 Shannon 熵 ≥ 3.5。
// 三个条件缺一不可——只看长度会把文件路径全报出来。
func looksOpaque(v string) bool {
	if len(v) < minEntropyLen || !opaquePattern.MatchString(v) {
		return false
	}
	return shannon(v) >= minEntropy
}

func shannon(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	for _, c := range s {
		counts[c]++
	}
	var h float64
	n := float64(len([]rune(s)))
	for _, c := range counts {
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// Mask 留前 4 后 4。太短的值全部遮掉——前 4 后 4 会把它整个露出来。
func Mask(v string) string {
	if len(v) < 12 {
		return "…"
	}
	return v[:4] + "…" + v[len(v)-4:]
}

// SuggestName 由键名派生凭据名：ANTHROPIC_AUTH_TOKEN → anthropic_auth_token，
// someApiKey → some_api_key。字符集与占位符语法一致（[A-Za-z0-9_-]+）。
func SuggestName(key string) string {
	var b strings.Builder
	prevLower := false
	for _, c := range key {
		switch {
		case unicode.IsUpper(c):
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
			prevLower = false
		case unicode.IsLower(c) || unicode.IsDigit(c):
			b.WriteRune(c)
			prevLower = true
		default:
			b.WriteByte('_')
			prevLower = false
		}
	}
	name := strings.Trim(collapseUnderscores(b.String()), "_")
	if name == "" {
		return "cred"
	}
	return name
}

func collapseUnderscores(s string) string {
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return s
}
