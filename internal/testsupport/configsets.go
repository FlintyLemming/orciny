//go:build testing

package testsupport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub"
)

// SeedConfigSet 建一个配置集、写入给定文件、发布 v1，返回 (setID, revID)。
//
// 实际实现在 hub.Hub.SeedConfigSet：Go 的 internal 规则不允许本包
// import hub/internal/*，因此业务落在 hub 公开包，这里只做转发。
func (h *TestHub) SeedConfigSet(t *testing.T, name string, files map[string]string) (string, string) {
	t.Helper()
	return h.Hub.SeedConfigSet(t, name, files)
}

// RequireAssignmentState 等到某机器的指派状态变成 want。
//
// 状态由 ApplyAck 的处理路径写入，发生在另一个 goroutine 上，
// 因此必须轮询而不是直接断言。
func (h *TestHub) RequireAssignmentState(t *testing.T, machineID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		recs, err := h.App.FindRecordsByFilter("assignments", "machine = {:m}", "", 1, 0,
			map[string]any{"m": machineID})
		return err == nil && len(recs) == 1 && recs[0].GetString("state") == want
	}, 3*time.Second, 10*time.Millisecond, "机器 %s 的指派未变成 %s", machineID, want)
}

// —— M1.5 服务绑定。同样只做转发，实现在 hub 公开包。——

// SeedProvider 建一条凭据 + 一条 AI 服务配置，返回 provider id。
func (h *TestHub) SeedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()
	return h.Hub.SeedProvider(t, name, baseURL, key)
}

// BindConfigSet 绑定并发布一条新 Revision，返回新 revision id。
func (h *TestHub) BindConfigSet(t *testing.T, setID, providerID, model string) string {
	t.Helper()
	return h.Hub.BindConfigSet(t, setID, providerID, model)
}

// ProviderInputOf 读回一条 Provider 的当前值，只把 base_url 换成新的。
func (h *TestHub) ProviderInputOf(t *testing.T, providerID, baseURL string) hub.ProviderInput {
	t.Helper()
	return h.Hub.ProviderInputOf(t, providerID, baseURL)
}

// RevisionBlob 读一条 Revision 里某个路径的 blob 内容。
func (h *TestHub) RevisionBlob(t *testing.T, revID, path string) []byte {
	t.Helper()
	return h.Hub.RevisionBlob(t, revID, path)
}
