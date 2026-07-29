package enroll_test

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/internal/clock"
)

var start = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) (*enroll.Service, *tests.TestApp, *clock.Fake) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	fake := clock.NewFake(start)
	return enroll.NewService(app, events.NewWriter(app), fake, 15*time.Minute, rand.Reader), app, fake
}

func TestIssueTokenReturnsPlaintextOnce(t *testing.T) {
	s, app, _ := newService(t)

	token, expires, err := s.IssueToken()
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, start.Add(15*time.Minute), expires.UTC())

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)

	// 明文绝不入库
	require.NotContains(t, recs[0].GetString("token_hash"), token)
	require.Equal(t, enroll.HashToken(token), recs[0].GetString("token_hash"))
	require.Empty(t, recs[0].GetString("used_at"))
}

func TestIssueTokenIsUnguessable(t *testing.T) {
	s, _, _ := newService(t)

	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		token, _, err := s.IssueToken()
		require.NoError(t, err)
		require.False(t, seen[token], "签发出重复 token")
		seen[token] = true
		require.GreaterOrEqual(t, len(token), 40, "32 字节 base64url 至少 43 字符")
		require.NotContains(t, token, "=", "URL 安全、无填充，便于放进命令行")
	}
}

func TestIssueTokenWritesEventWithPrefixOnly(t *testing.T) {
	s, app, _ := newService(t)

	token, _, err := s.IssueToken()
	require.NoError(t, err)

	recs, err := app.FindRecordsByFilter("events", "kind = 'token.issued'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)

	var detail map[string]any
	require.NoError(t, recs[0].UnmarshalJSONField("detail", &detail))
	prefix, _ := detail["token_prefix"].(string)
	require.Len(t, prefix, 8, "只记前 8 位（spec §7.5）")
	require.True(t, strings.HasPrefix(token, prefix))
}

func TestIssueTokenHonoursTTL(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	fake := clock.NewFake(start)
	s := enroll.NewService(app, events.NewWriter(app), fake, time.Hour, rand.Reader)

	_, expires, err := s.IssueToken()
	require.NoError(t, err)
	require.Equal(t, start.Add(time.Hour), expires.UTC())
}

func TestPurgeExpiredTokensRemovesOldOnes(t *testing.T) {
	s, app, fake := newService(t)

	_, _, err := s.IssueToken()
	require.NoError(t, err)

	// 过期后又过了一天多
	fake.Advance(15*time.Minute + 25*time.Hour)

	n, err := s.PurgeExpiredTokens(24 * time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Empty(t, recs)
}

func TestPurgeExpiredTokensKeepsRecentOnes(t *testing.T) {
	s, app, fake := newService(t)

	_, _, err := s.IssueToken()
	require.NoError(t, err)

	// 已过期，但还在保留窗口内 —— 排查「刚才那次安装」时还用得上
	fake.Advance(15*time.Minute + time.Hour)

	n, err := s.PurgeExpiredTokens(24 * time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
}

func TestPurgeExpiredTokensKeepsValidOnes(t *testing.T) {
	s, app, _ := newService(t)
	_, _, err := s.IssueToken()
	require.NoError(t, err)

	n, err := s.PurgeExpiredTokens(24 * time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
}

func TestHashTokenIsHexSHA256(t *testing.T) {
	h := enroll.HashToken("abc")
	require.Len(t, h, 64)
	require.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", h)
}
