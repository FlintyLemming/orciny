package providers

import (
	"net/url"
	"strings"
)

// NormalizeURL 归一化 base_url（spec §6.2 / §6.4）：
// 去首尾空白、去尾斜杠、scheme 与 host 小写、去默认端口。
//
// 路径**不动大小写**：有的中转拿路径段区分产品线。
// 解不动的原样返回（只去空白）——吞掉用户输入比留着一个奇怪的字符串更糟。
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = normalizeHost(u.Scheme, u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

// HostOf 返回归一化后的 host（含非默认端口）。解不动返回空串。
func HostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return normalizeHost(strings.ToLower(u.Scheme), u.Host)
}

func normalizeHost(scheme, host string) string {
	h := strings.ToLower(host)
	switch {
	case scheme == "https" && strings.HasSuffix(h, ":443"):
		return strings.TrimSuffix(h, ":443")
	case scheme == "http" && strings.HasSuffix(h, ":80"):
		return strings.TrimSuffix(h, ":80")
	}
	return h
}
