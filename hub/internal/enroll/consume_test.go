package enroll_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/protocol"
)

func newKey(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, protocol.EncodePublicKey(pub)
}

func req(token, pubKey string) enroll.Request {
	return enroll.Request{
		Token: token, PubKey: pubKey,
		Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0",
	}
}

func TestEnrollHappyPath(t *testing.T) {
	s, app, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	pub, pubKey := newKey(t)

	res, err := s.Enroll(req(token, pubKey))
	require.NoError(t, err)
	require.False(t, res.ReEnrolled)
	require.False(t, res.Replayed)
	require.Equal(t, protocol.Fingerprint(pub), res.Fingerprint)

	m, err := app.FindRecordById("machines", res.MachineID)
	require.NoError(t, err)
	require.Equal(t, "box", m.GetString("name"), "备注名默认取 hostname")
	require.Equal(t, "box", m.GetString("hostname"))
	require.Equal(t, "linux", m.GetString("os"))
	require.Equal(t, "arm64", m.GetString("arch"))
	require.Equal(t, "0.1.0", m.GetString("agent_version"))
	require.Equal(t, pubKey, m.GetString("pub_key"))
	require.Equal(t, "offline", m.GetString("status"), "刚 enroll 还没连上，必须是 offline")

	// token 已核销并回填了机器
	tk, err := app.FindFirstRecordByData("enroll_tokens", "token_hash", enroll.HashToken(token))
	require.NoError(t, err)
	require.NotEmpty(t, tk.GetString("used_at"))
	require.Equal(t, res.MachineID, tk.GetString("machine"))

	// 事件
	evs, err := app.FindRecordsByFilter("events", "kind = 'machine.enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, evs, 1)
	require.Equal(t, res.MachineID, evs[0].GetString("machine"))
}

func TestEnrollRejectsExpiredToken(t *testing.T) {
	s, _, fake := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	_, pubKey := newKey(t)

	fake.Advance(15*time.Minute + time.Second)

	_, err = s.Enroll(req(token, pubKey))
	require.ErrorIs(t, err, enroll.ErrTokenExpired)
}

func TestEnrollRejectsUnknownToken(t *testing.T) {
	s, _, _ := newService(t)
	_, pubKey := newKey(t)

	_, err := s.Enroll(req("从没签发过的 token", pubKey))
	require.ErrorIs(t, err, enroll.ErrTokenInvalid)
}

func TestEnrollRejectsUsedTokenWithDifferentKey(t *testing.T) {
	s, _, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)

	_, first := newKey(t)
	_, err = s.Enroll(req(token, first))
	require.NoError(t, err)

	// 偷到已用 token 的攻击者拿自己的公钥来注册 —— 必须拒绝
	_, second := newKey(t)
	_, err = s.Enroll(req(token, second))
	require.ErrorIs(t, err, enroll.ErrTokenUsed)
}

// 响应在网络上丢了：agent 手里有私钥却没有 hub 公钥，token 又已核销。
// 重放同一请求必须成功，否则这台机器就卡死了（spec §4.4）。
func TestEnrollIdempotentReplay(t *testing.T) {
	s, app, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	_, pubKey := newKey(t)

	first, err := s.Enroll(req(token, pubKey))
	require.NoError(t, err)

	second, err := s.Enroll(req(token, pubKey))
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, first.MachineID, second.MachineID)
	require.Equal(t, first.Fingerprint, second.Fingerprint)

	machines, err := app.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 1, "重放不得建出第二台机器")
}

// 重装 agent 是常见操作：指纹已存在时更新而非重复创建，
// 但仍然要求一枚有效 token —— 否则任何人都能用伪造公钥覆盖已有机器。
func TestReEnrollUpdatesExistingMachine(t *testing.T) {
	s, app, _ := newService(t)
	pub, pubKey := newKey(t)

	t1, _, err := s.IssueToken()
	require.NoError(t, err)
	first, err := s.Enroll(req(t1, pubKey))
	require.NoError(t, err)

	// 用户改过备注名，re-enroll 不该覆盖它
	m, err := app.FindRecordById("machines", first.MachineID)
	require.NoError(t, err)
	m.Set("name", "我的构建机")
	require.NoError(t, app.Save(m))

	t2, _, err := s.IssueToken()
	require.NoError(t, err)
	second, err := s.Enroll(enroll.Request{
		Token: t2, PubKey: pubKey,
		Hostname: "box-renamed", OS: "linux", Arch: "arm64", AgentVersion: "0.2.0",
	})
	require.NoError(t, err)
	require.True(t, second.ReEnrolled)
	require.Equal(t, first.MachineID, second.MachineID)
	require.Equal(t, protocol.Fingerprint(pub), second.Fingerprint)

	m2, err := app.FindRecordById("machines", first.MachineID)
	require.NoError(t, err)
	require.Equal(t, "我的构建机", m2.GetString("name"), "用户改的备注名必须保留")
	require.Equal(t, "box-renamed", m2.GetString("hostname"))
	require.Equal(t, "0.2.0", m2.GetString("agent_version"))

	evs, err := app.FindRecordsByFilter("events", "kind = 'machine.re-enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, evs, 1)
}

func TestReEnrollWithoutTokenIsRejected(t *testing.T) {
	s, _, _ := newService(t)
	_, pubKey := newKey(t)

	t1, _, err := s.IssueToken()
	require.NoError(t, err)
	_, err = s.Enroll(req(t1, pubKey))
	require.NoError(t, err)

	_, err = s.Enroll(req("", pubKey))
	require.Error(t, err, "没有有效 token 就不能改写已有机器的公钥")
}

// 同一 token 并发两次，恰好一个成功。
func TestConcurrentEnrollExactlyOneWins(t *testing.T) {
	s, app, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)

	_, keyA := newKey(t)
	_, keyB := newKey(t)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	keys := []string{keyA, keyB}
	start := make(chan struct{})

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = s.Enroll(req(token, keys[i]))
		}(i)
	}
	close(start)
	wg.Wait()

	okCount := 0
	for _, err := range errs {
		if err == nil {
			okCount++
		}
	}
	require.Equal(t, 1, okCount, "恰好一个请求应当成功，实际 %d 个：%v", okCount, errs)

	machines, err := app.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 1, "一枚 token 只能建出一台机器")
}

func TestEnrollRejectsBadPubKey(t *testing.T) {
	s, _, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)

	_, err = s.Enroll(req(token, "不是 base64 公钥"))
	require.ErrorIs(t, err, enroll.ErrBadPubKey)
}

func TestEnrollRejectsEmptyFields(t *testing.T) {
	s, _, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	_, pubKey := newKey(t)

	_, err = s.Enroll(enroll.Request{Token: token, PubKey: pubKey})
	require.ErrorIs(t, err, enroll.ErrBadRequest)
}
