package drift

import (
	"bytes"
	"strings"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// baseURLToken 是基线里 base_url 那一处的字面形态。
const baseURLToken = "{{provider.base_url}}"

// DetectBindingDrift 识别绑定漂移（M1.5 spec §6.1）：
// **基线侧是 {{provider.base_url}} 占位符、现状侧是字面值**。
// 命中时返回现状侧那段字面 URL。
//
// 做法是按行比：基线里每一处占位符都落在某一行上，取它在这一行里的前缀
// 与后缀，去现状内容里找同前缀同后缀的行——中间那段就是机器上实际写的
// URL。按行而不是按字节偏移，是因为用户多半只改了那一行，其余行还对得上。
//
// 识别逻辑完全在 hub，agent 不需要知道「绑定」这个概念。
func DetectBindingDrift(base, cur []byte) (string, bool) {
	if len(cur) == 0 || !bytes.Contains(base, []byte(baseURLToken)) {
		return "", false
	}
	curLines := strings.Split(string(cur), "\n")

	for _, bl := range strings.Split(string(base), "\n") {
		i := strings.Index(bl, baseURLToken)
		if i < 0 {
			continue
		}
		trimmedBase := strings.TrimSpace(bl)
		prefix := strings.TrimLeft(bl[:i], " \t")
		suffix := strings.TrimRight(bl[i+len(baseURLToken):], " \t\r")

		for _, cl := range curLines {
			t := strings.TrimSpace(cl)
			if t == trimmedBase {
				continue // 这一行没被改，还是占位符
			}
			if !strings.HasPrefix(t, prefix) || !strings.HasSuffix(t, suffix) {
				continue
			}
			mid := strings.TrimSuffix(t[len(prefix):], suffix)
			mid = strings.Trim(mid, "\"' \t")
			if mid == "" || strings.Contains(mid, "{{") {
				continue
			}
			// 不像 URL 就别乱报——用户可能只是把值清空了或写了句中文。
			if providers.HostOf(mid) == "" {
				continue
			}
			return mid, true
		}
	}
	return "", false
}
