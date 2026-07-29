// Package enroll 实现注册 token 的签发与核销，以及机器记录的建立。
//
// 信任根落在那条 curl 命令上（spec §4.4）：走 HTTPS、token 一次性、
// 15 分钟有效。攻击者要冒充 hub，必须恰好在 enroll 那一刻做中间人且
// 持有有效证书；钉扎完成后即免疫。
package enroll

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// TokenBytes 是注册 token 的随机字节数。
const TokenBytes = 32

// Service 是 enroll 的业务逻辑，全部数据库访问都在这里，路由层只做编解码。
type Service struct {
	app core.App
	ev  *events.Writer
	clk clock.Clock
	ttl time.Duration
	rnd io.Reader
}

func NewService(app core.App, ev *events.Writer, clk clock.Clock, ttl time.Duration, rnd io.Reader) *Service {
	return &Service{app: app, ev: ev, clk: clk, ttl: ttl, rnd: rnd}
}

// HashToken 是 token 在库里的唯一表示。明文只在签发响应里出现一次。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueToken 签发一枚一次性注册 token，返回明文与过期时刻。
func (s *Service) IssueToken() (string, time.Time, error) {
	raw := make([]byte, TokenBytes)
	if _, err := io.ReadFull(s.rnd, raw); err != nil {
		return "", time.Time{}, fmt.Errorf("enroll: 生成 token: %w", err)
	}
	// base64url 无填充：可以直接贴进命令行，不需要引号。
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := s.clk.Now().Add(s.ttl)

	c, err := s.app.FindCollectionByNameOrId("enroll_tokens")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("enroll: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("token_hash", HashToken(token))
	r.Set("expires_at", expires.UTC())
	if err := s.app.Save(r); err != nil {
		return "", time.Time{}, fmt.Errorf("enroll: 保存 token: %w", err)
	}

	// 只记前 8 位（spec §7.5）——够用来在事件流里对上号，又泄不出去。
	if err := s.ev.Write(events.KindTokenIssued, "", map[string]any{
		"token_prefix": token[:8],
		"expires_at":   expires.UTC().Format(time.RFC3339),
	}); err != nil {
		s.app.Logger().Warn("写 token.issued 事件失败", "error", err)
	}

	return token, expires, nil
}

// PurgeExpiredTokens 删除过期超过 olderThan 的 token 记录（spec §8）。
// 返回删除的行数。
//
// 保留一段时间而不是一过期就删：排查「刚才那次安装为什么失败」时，
// 事件流里的 token 前缀要能对上一条真实记录。
func (s *Service) PurgeExpiredTokens(olderThan time.Duration) (int64, error) {
	cutoff, err := types.ParseDateTime(s.clk.Now().UTC().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("enroll: 格式化时间: %w", err)
	}
	res, err := s.app.DB().NewQuery(`
		DELETE FROM {{enroll_tokens}} WHERE expires_at < {:cutoff}
	`).Bind(dbx.Params{"cutoff": cutoff.String()}).Execute()
	if err != nil {
		return 0, fmt.Errorf("enroll: 清理过期 token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("enroll: 读取清理结果: %w", err)
	}
	return n, nil
}
