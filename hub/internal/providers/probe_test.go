package providers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// 信号阶梯（探测设计）。判定看 **content-type 而不是状态码**：
// 中转站把没匹配上的路径交给前端 SPA 兜底，回的是 200 + text/html，
// 只看状态码会把「路径填错」当成「探测成功」。
func TestClassify(t *testing.T) {
	for _, c := range []struct {
		name        string
		status      int
		contentType string
		want        providers.Signal
	}{
		{"路径对 key 对", 200, "application/json; charset=utf-8", providers.SignalOK},
		{"路径对 key 错", 401, "application/json; charset=utf-8", providers.SignalAuthFailed},
		{"key 无权限", 403, "application/json", providers.SignalAuthFailed},
		{"前端兜底页", 200, "text/html; charset=utf-8", providers.SignalNotAPI},
		{"API 认得但没这条路由", 404, "application/json", providers.SignalNoRoute},
		{"方法不对", 405, "application/json", providers.SignalNoRoute},
		{"网关挂了", 502, "text/html", providers.SignalNoRoute},
		{"没有 content-type 的 200", 200, "", providers.SignalNotAPI},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, providers.Classify(c.status, c.contentType))
		})
	}
}

// 「路径确认了吗」独立于「key 对不对」：401 说明请求确实打到了 API 的路由上，
// 这个候选就是对的，不必再往下试。
func TestSignalConfirmsPath(t *testing.T) {
	require.True(t, providers.SignalOK.ConfirmsPath())
	require.True(t, providers.SignalAuthFailed.ConfirmsPath())
	require.False(t, providers.SignalNotAPI.ConfirmsPath())
	require.False(t, providers.SignalNoRoute.ConfirmsPath())
	require.False(t, providers.SignalUnreachable.ConfirmsPath())
}

// 候选顺序：原样在最前——用户填对了就该一次命中，不该先去试我们猜的路径。
// 紧跟着是根地址，因为「把带路径的地址填进了另一个协议的框」是对称的常见错法。
func TestCandidatesStartWithInputThenRoot(t *testing.T) {
	got := providers.Candidates(providers.EndpointOpenAI, "https://x.test/v1")
	require.Equal(t, "https://x.test/v1", got[0])
	require.Equal(t, "https://x.test", got[1])
}

// 输入本身就是根地址时不该产生重复的候选。
func TestCandidatesDoNotRepeatRoot(t *testing.T) {
	got := providers.Candidates(providers.EndpointOpenAI, "https://x.test")
	require.Equal(t, "https://x.test", got[0])
	require.Equal(t, len(got), len(uniq(got)), "候选有重复: %v", got)
}

// 后缀语料来自预设表：加一条预设就等于教会探测器一个新的路径约定。
func TestCandidateSuffixesComeFromPresets(t *testing.T) {
	oa := providers.Candidates(providers.EndpointOpenAI, "https://x.test")
	require.Contains(t, oa, "https://x.test/v1")
	require.Contains(t, oa, "https://x.test/api/paas/v4")

	cl := providers.Candidates(providers.EndpointClaude, "https://x.test")
	require.Contains(t, cl, "https://x.test/anthropic")
	require.Contains(t, cl, "https://x.test/api/anthropic")

	// 两个端点的后缀不该串味：OpenAI 侧不试 /anthropic。
	require.NotContains(t, oa, "https://x.test/anthropic")
}

// 平手时短路径优先：短的那个通常是通用约定（/v1），
// 长的是某一家的自定义路径（/api/paas/v4）。按字典序平手会让通用约定排到后面，
// 白白多发一次请求。
func TestCandidatesPreferShorterPathOnTie(t *testing.T) {
	got := providers.Candidates(providers.EndpointOpenAI, "https://x.test")
	require.Less(t, indexOf(got, "https://x.test/v1"), indexOf(got, "https://x.test/api/paas/v4"))
}

func indexOf(in []string, want string) int {
	for i, s := range in {
		if s == want {
			return i
		}
	}
	return -1
}

// 已经以某后缀结尾时不再叠加同一个后缀，别去试 /v1/v1。
func TestCandidatesDoNotStackSameSuffix(t *testing.T) {
	got := providers.Candidates(providers.EndpointOpenAI, "https://x.test/v1")
	require.NotContains(t, got, "https://x.test/v1/v1")
}

// 解不动的输入不产生候选——交给上层报错，不要拿它去发请求。
func TestCandidatesRejectNonURL(t *testing.T) {
	require.Empty(t, providers.Candidates(providers.EndpointOpenAI, "不是 URL"))
	require.Empty(t, providers.Candidates(providers.EndpointOpenAI, ""))
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// relay 复刻中转站的真实行为（对 new-api 实测得来）：
// 认得的路由回 JSON，认不得的一律交给前端 SPA 兜底 —— 200 + text/html。
// 正是这个兜底让「base_url 少了一段」无法靠状态码发现。
func relay(t *testing.T, routes map[string]func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := routes[r.URL.Path]; ok {
			h(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>控制台</body></html>"))
	}))
	t.Cleanup(s.Close)
	return s
}

func modelList(ids ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]any{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	}
}

// 本功能要解决的那个场景：用户把根地址填进 OpenAI 框。
// 根地址下的 /models 撞上兜底页，探测该退到 /v1 并补全。
func TestProbeCompletesMissingV1OnOpenAI(t *testing.T) {
	s := relay(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": modelList("glm-5", "kimi-k2.6"),
	})

	got := providers.Probe(context.Background(), providers.NewProbeClient(),
		providers.EndpointOpenAI, s.URL, "sk-test")

	require.Equal(t, providers.SignalOK, got.Signal)
	require.Equal(t, s.URL+"/v1", got.BaseURL, "应补全成 /v1")
	require.Equal(t, []string{"glm-5", "kimi-k2.6"}, got.Models)
}

// 同一个根地址在 Claude 侧本来就是对的（客户端自己会补 /v1/messages），
// 探测不该无事生非地改写它。
func TestProbeLeavesCorrectInputAlone(t *testing.T) {
	s := relay(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": modelList("glm-5"),
	})

	got := providers.Probe(context.Background(), providers.NewProbeClient(),
		providers.EndpointClaude, s.URL, "sk-test")

	require.Equal(t, providers.SignalOK, got.Signal)
	require.Equal(t, s.URL, got.BaseURL, "原样就对，不该改写")
}

// 401 确认路径、但拿不到模型清单。这仍是有用的结果：
// 地址补全了，用户只需回去换 key。
func TestProbeConfirmsPathOnAuthFailure(t *testing.T) {
	s := relay(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Invalid token"}}`))
		},
	})

	got := providers.Probe(context.Background(), providers.NewProbeClient(),
		providers.EndpointOpenAI, s.URL+"/v1", "sk-wrong")

	require.Equal(t, providers.SignalAuthFailed, got.Signal)
	require.Equal(t, s.URL+"/v1", got.BaseURL)
	require.Empty(t, got.Models)
}

// 一个候选都没确认时**不改写用户输入**，只如实报告试过哪些。
// 这与 NormalizeURL 的立场一致：吞掉用户输入比留着一个奇怪的字符串更糟。
func TestProbeDoesNotRewriteWhenNothingConfirms(t *testing.T) {
	s := relay(t, nil) // 全都撞兜底页

	got := providers.Probe(context.Background(), providers.NewProbeClient(),
		providers.EndpointOpenAI, s.URL, "sk-test")

	require.Equal(t, providers.SignalNotAPI, got.Signal)
	require.Empty(t, got.BaseURL, "没确认就不要给出地址")
	require.NotEmpty(t, got.Tried, "该如实报告试过哪些")
}

// 两个协议的鉴权头不同，探测必须按端点发对应的头，
// 否则 401 会被误判成「key 不对」而不是「头发错了」。
func TestProbeSendsProtocolSpecificHeaders(t *testing.T) {
	var gotHeaders http.Header
	capture := func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		modelList("m")(w, r)
	}
	s := relay(t, map[string]func(http.ResponseWriter, *http.Request){"/v1/models": capture})

	providers.Probe(context.Background(), providers.NewProbeClient(),
		providers.EndpointClaude, s.URL, "sk-claude")
	require.Equal(t, "sk-claude", gotHeaders.Get("x-api-key"))
	require.NotEmpty(t, gotHeaders.Get("anthropic-version"))
	require.Empty(t, gotHeaders.Get("Authorization"))

	providers.Probe(context.Background(), providers.NewProbeClient(),
		providers.EndpointOpenAI, s.URL+"/v1", "sk-openai")
	require.Equal(t, "Bearer sk-openai", gotHeaders.Get("Authorization"))
	require.Empty(t, gotHeaders.Get("x-api-key"))
}

// 不跟跨主机跳转：探测地址由用户提供，跟到别的主机就把 key 送给了第三方。
func TestProbeDoesNotFollowCrossHostRedirect(t *testing.T) {
	var leaked bool
	other := relay(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": func(w http.ResponseWriter, r *http.Request) {
			leaked = r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != ""
			modelList("m")(w, r)
		},
	})
	s := relay(t, map[string]func(http.ResponseWriter, *http.Request){
		"/v1/models": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+"/v1/models", http.StatusFound)
		},
	})

	got := providers.Probe(context.Background(), providers.NewProbeClient(),
		providers.EndpointOpenAI, s.URL+"/v1", "sk-test")

	require.False(t, leaked, "key 不该被送到跳转目标主机")
	require.NotEqual(t, providers.SignalOK, got.Signal)
}

// Status 是给前端的机器可读判定。UI 要按它分支出四种不同的措辞
// （采用 / 换 key / 地址不对 / 连不上），所以它是稳定的 wire 值，不是调试字符串。
func TestSignalStatus(t *testing.T) {
	require.Equal(t, "ok", providers.SignalOK.Status())
	require.Equal(t, "auth_failed", providers.SignalAuthFailed.Status())
	require.Equal(t, "not_api", providers.SignalNotAPI.Status())
	require.Equal(t, "no_route", providers.SignalNoRoute.Status())
	require.Equal(t, "unreachable", providers.SignalUnreachable.Status())
}
