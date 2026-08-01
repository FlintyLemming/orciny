//go:build testing

package hub

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
)

// SeedConfigSet 建一个配置集、写入给定文件、发布 v1，返回 (setID, revID)。
//
// 放在 hub 公开包而不是 internal/testsupport：Go 的 internal 规则不允许
// testsupport（位于顶层 internal/）import hub/internal/*。testsupport 通过
// TestHub 转发到这里。
//
// files 的键是受管相对路径（以 HOME 为根），值是内容。权限位统一 0644；
// 需要 0600 的用例自行改写 revision。
func (h *Hub) SeedConfigSet(t *testing.T, name string, files map[string]string) (string, string) {
	t.Helper()

	b := blobs.New(h.App)
	ev := events.NewWriter(h.App)
	cs := configsets.NewService(h.App, b, ev)
	rs := revisions.NewService(h.App, b, ev)

	set, err := cs.Create(name, "由 testsupport 生成")
	require.NoError(t, err, "创建配置集")
	for path, content := range files {
		_, err := cs.SetDraftFile(set.Id, path, []byte(content), 0o644, nil)
		require.NoError(t, err, "写草稿 %s", path)
	}
	rev, err := rs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err, "发布")
	return set.Id, rev.Id
}
