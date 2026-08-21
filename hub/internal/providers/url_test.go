package providers_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

func TestNormalizeURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://open.bigmodel.cn/api/anthropic/", "https://open.bigmodel.cn/api/anthropic"},
		{"https://OPEN.BigModel.CN/api/anthropic", "https://open.bigmodel.cn/api/anthropic"},
		{"https://open.bigmodel.cn:443/api/anthropic", "https://open.bigmodel.cn/api/anthropic"},
		{"http://localhost:80/anthropic", "http://localhost/anthropic"},
		{"http://localhost:8080/anthropic", "http://localhost:8080/anthropic"},
		{"  https://api.z.ai/api/anthropic  ", "https://api.z.ai/api/anthropic"},
		{"https://api.moonshot.cn", "https://api.moonshot.cn"},
		// 路径大小写不动：有的中转拿它区分产品线。
		{"https://x.test/API/Anthropic", "https://x.test/API/Anthropic"},
		// 解不动的原样返回（去空白），不要吞掉用户输入。
		{"不是 URL", "不是 URL"},
	} {
		require.Equal(t, c.want, providers.NormalizeURL(c.in), "输入 %q", c.in)
	}
}

func TestHostOf(t *testing.T) {
	require.Equal(t, "open.bigmodel.cn",
		providers.HostOf("https://OPEN.bigmodel.CN:443/api/anthropic/"))
	require.Equal(t, "localhost:8080", providers.HostOf("http://localhost:8080/x"))
	require.Equal(t, "", providers.HostOf("不是 URL"))
}
