package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Signal 是一次探测请求的判定结果。
//
// 判定的依据是 **content-type，不是状态码**：中转站通常把没匹配上的路径
// 交给前端 SPA 兜底，回的是 200 + text/html。只看状态码会把「base_url 少了
// 一段路径」这种错当成探测成功——而这正是本功能要抓的那个坑。
type Signal int

const (
	SignalUnreachable Signal = iota // 连不上：DNS、超时、TLS
	SignalNotAPI                    // 200 但不是 JSON：撞上了前端兜底页
	SignalNoRoute                   // API 认得这个域名，但没有这条路由
	SignalAuthFailed                // 路径对，key 不对
	SignalOK                        // 路径对，key 对
)

// ConfirmsPath 表示「这个候选路径是对的」，与 key 对不对无关。
// 401 同样确认路径——请求确实打到了 API 的路由上，不必再往下试候选。
func (s Signal) ConfirmsPath() bool {
	return s == SignalOK || s == SignalAuthFailed
}

// Status 是给前端的机器可读判定。UI 按它分支出四种措辞
// （采用 / 换 key / 地址不对 / 连不上），因此这是稳定的 wire 值，
// 不是调试用的字符串，改动等于改前端契约。
func (s Signal) Status() string {
	switch s {
	case SignalOK:
		return "ok"
	case SignalAuthFailed:
		return "auth_failed"
	case SignalNotAPI:
		return "not_api"
	case SignalNoRoute:
		return "no_route"
	default:
		return "unreachable"
	}
}

// Classify 把一次 HTTP 响应归到信号阶梯上。
func Classify(status int, contentType string) Signal {
	json := strings.Contains(strings.ToLower(contentType), "json")
	switch {
	case status == 401 || status == 403:
		return SignalAuthFailed
	case status == 200 && json:
		return SignalOK
	case status == 200:
		return SignalNotAPI
	default:
		return SignalNoRoute
	}
}

// maxCandidates 是一次探测最多试几个地址。点一次按钮就串行发这么多请求，
// 命中即停——常见情形（填对了、或只差一段）在前两个就结束。
const maxCandidates = 8

// Candidates 生成待试的 base_url，按「最可能正确」排序。
//
// 原样排第一：用户填对了就该一次命中，不该先去试我们猜的路径。
// 根地址排第二：把带路径的地址填进另一个协议的框是对称的常见错法
// （把 .../v1 填进 Claude 框，或把根地址填进 OpenAI 框）。
//
// 解不动的输入返回空——交给上层报错，不要拿它去发请求。
func Candidates(endpoint, raw string) []string {
	base := NormalizeURL(raw)
	if HostOf(base) == "" {
		return nil
	}
	root := rootOf(base)

	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] && len(out) < maxCandidates {
			seen[s] = true
			out = append(out, s)
		}
	}

	add(base)
	add(root)
	for _, p := range presetPaths(endpoint) {
		// 已经以这个后缀结尾就别叠加，不去试 /v1/v1。
		if !strings.HasSuffix(base, p) {
			add(base + p)
		}
		add(root + p)
	}
	return out
}

// presetPaths 是预设表里该协议端点用过的路径段，按出现频次降序、
// 同频次按字典序（保证确定性）。
//
// 语料直接从 presets 派生而不另立一张表：加一条预设就等于教会探测器
// 一个新的路径约定，不会出现两处各记一半的情况。
func presetPaths(endpoint string) []string {
	count := map[string]int{}
	for _, p := range presets {
		raw := p.OpenAI.BaseURL
		if endpoint == EndpointClaude {
			raw = p.Claude.BaseURL
		}
		if raw == "" {
			continue
		}
		// 空路径来自「域名即端点」的平台（Anthropic 官方），
		// 那种情形已经由 base / root 两个候选覆盖。
		if path := pathOf(raw); path != "" {
			count[path]++
		}
	}
	out := make([]string, 0, len(count))
	for p := range count {
		out = append(out, p)
	}
	// 频次高的先试；平手时短路径优先——短的通常是通用约定（/v1），
	// 长的是某一家的自定义路径（/api/paas/v4）。最后按字典序保证确定性。
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// rootOf 返回 scheme://host，去掉路径。解不动返回空串。
func rootOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + normalizeHost(strings.ToLower(u.Scheme), u.Host)
}

// pathOf 返回去掉尾斜杠的路径部分。
func pathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimRight(u.Path, "/")
}

// probeTimeout 是整次探测（含所有候选）的上限。
const probeTimeout = 10 * time.Second

// maxProbeBody 限制读取的响应体大小。探测只关心模型清单，
// 没有理由把一个不明来源的地址返回的任意大响应全读进内存。
const maxProbeBody = 1 << 20

// anthropicVersion 是 Anthropic 协议的必填版本头。
const anthropicVersion = "2023-06-01"

// Result 是一次探测的结论。
//
// BaseURL 只在**确认到路径**时才非空。一个候选都没确认时它是空的，
// 由 UI 原样保留用户的输入——与 NormalizeURL 同一个立场：
// 吞掉用户输入比留着一个奇怪的字符串更糟。
type Result struct {
	BaseURL string   `json:"base_url"`
	Models  []string `json:"models"`
	Tried   []string `json:"tried"`
	Signal  Signal   `json:"-"`
}

// NewProbeClient 造探测专用的 HTTP client。
//
// 探测地址来自用户输入，因此**不跟跨主机跳转**：x-api-key 这类自定义头
// 不在 Go 的跨域剥离名单里，跟过去就等于把 key 交给了跳转目标。
// 同主机跳转（http→https 一类）照常跟。
func NewProbeClient() *http.Client {
	return &http.Client{
		Timeout: probeTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("providers: 重定向过多")
			}
			if req.URL.Host != via[0].URL.Host {
				return errors.New("providers: 拒绝跨主机跳转")
			}
			return nil
		},
	}
}

// Probe 依次试候选地址，首个确认路径的即停并返回。
//
// 探的是 GET <base>/models（OpenAI 形）或 GET <base>/v1/models（Anthropic 形）：
// 两个协议都有这条路由、不花 token，而且成功时顺带把模型清单带回来——
// 表单本来就要填这个清单。
func Probe(ctx context.Context, client *http.Client, endpoint, raw, key string) Result {
	out := Result{Signal: SignalUnreachable}
	for _, base := range Candidates(endpoint, raw) {
		out.Tried = append(out.Tried, base)
		sig, models := probeOne(ctx, client, endpoint, base, key)
		if sig > out.Signal {
			out.Signal = sig
		}
		if sig.ConfirmsPath() {
			out.BaseURL = base
			out.Models = models
			out.Signal = sig
			return out
		}
	}
	return out
}

// probeOne 打一个候选。网络层任何失败都归到 SignalUnreachable——
// 对调用方来说「连不上」和「连上了但不对」要分开，前者不该让用户去改地址。
func probeOne(
	ctx context.Context, client *http.Client, endpoint, base, key string,
) (Signal, []string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL(endpoint, base), nil)
	if err != nil {
		return SignalUnreachable, nil
	}
	if endpoint == EndpointClaude {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", anthropicVersion)
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := client.Do(req)
	if err != nil {
		return SignalUnreachable, nil
	}
	defer resp.Body.Close()

	sig := Classify(resp.StatusCode, resp.Header.Get("Content-Type"))
	if sig != SignalOK {
		return sig, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return SignalUnreachable, nil
	}
	return SignalOK, modelIDs(body)
}

// modelsURL 是探测实际要打的地址。它跟着**客户端的拼接习惯**走：
// Anthropic 客户端自己补 /v1/messages，所以 base 之后还有 /v1；
// OpenAI 客户端只补 /chat/completions，/v1 得由 base 自己带上。
// 这个不对称正是「两个框不能填同一个地址」的根源。
func modelsURL(endpoint, base string) string {
	if endpoint == EndpointClaude {
		return base + "/v1/models"
	}
	return base + "/models"
}

// modelIDs 从模型清单里取 id。两个协议的条目字段不同
// （OpenAI 是 object/owned_by，Anthropic 是 type/display_name），
// 但 data[].id 是共同的，探测只需要它。
func modelIDs(body []byte) []string {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	out := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
