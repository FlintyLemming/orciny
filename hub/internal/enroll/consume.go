package enroll

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

// Request 是一次 enroll 的输入。字段与 POST /api/orciny/enroll 的 body 一一对应。
type Request struct {
	Token        string
	PubKey       string
	Hostname     string
	OS           string
	Arch         string
	AgentVersion string
}

// Result 是 enroll 的结果。ReEnrolled 与 Replayed 只影响日志与事件，
// 对 agent 而言三种成功路径没有区别。
type Result struct {
	MachineID   string
	Fingerprint string
	ReEnrolled  bool
	Replayed    bool
}

// Enroll 核销 token 并建立/更新机器记录，整个过程在一个事务里。
func (s *Service) Enroll(req Request) (*Result, error) {
	if req.Token == "" || req.Hostname == "" || req.OS == "" || req.Arch == "" || req.AgentVersion == "" {
		return nil, ErrBadRequest
	}
	pub, err := protocol.DecodePublicKey(req.PubKey)
	if err != nil {
		return nil, ErrBadPubKey
	}
	fingerprint := protocol.Fingerprint(pub)
	hash := HashToken(req.Token)
	now, err := types.ParseDateTime(s.clk.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("enroll: 格式化时间: %w", err)
	}

	var result Result
	txErr := s.app.RunInTransaction(func(tx core.App) error {
		// 核销：条件 UPDATE 是唯一的并发仲裁点。
		// 「先 SELECT 再 UPDATE」在两个请求同时到达时会双双通过检查。
		res, err := tx.DB().NewQuery(`
			UPDATE {{enroll_tokens}}
			SET used_at = {:now}
			WHERE token_hash = {:hash} AND used_at = '' AND expires_at > {:now}
		`).Bind(dbx.Params{"now": now.String(), "hash": hash}).Execute()
		if err != nil {
			return fmt.Errorf("enroll: 核销 token: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("enroll: 读取核销结果: %w", err)
		}

		if affected == 0 {
			return s.handleNotConsumed(tx, hash, req.PubKey, &result)
		}

		machineID, reEnrolled, err := s.upsertMachine(tx, req, fingerprint)
		if err != nil {
			return err
		}
		result = Result{MachineID: machineID, Fingerprint: fingerprint, ReEnrolled: reEnrolled}

		// 回填 token → machine，供幂等重放判定
		tk, err := tx.FindFirstRecordByData("enroll_tokens", "token_hash", hash)
		if err != nil {
			return fmt.Errorf("enroll: 回读 token: %w", err)
		}
		tk.Set("machine", machineID)
		if err := tx.Save(tk); err != nil {
			return fmt.Errorf("enroll: 回填 token 的机器关联: %w", err)
		}

		kind := events.KindMachineEnrolled
		if reEnrolled {
			kind = events.KindMachineReEnrolled
		}
		return s.ev.WriteTx(tx, kind, machineID, map[string]any{
			"fingerprint":   fingerprint,
			"hostname":      req.Hostname,
			"agent_version": req.AgentVersion,
		})
	})
	if txErr != nil {
		return nil, txErr
	}
	return &result, nil
}

// handleNotConsumed 处理「没抢到 token」的三种情况：不存在、过期、已核销。
// 已核销时还要判断是不是同一次 enroll 的幂等重放。
func (s *Service) handleNotConsumed(tx core.App, hash, pubKey string, out *Result) error {
	tk, err := tx.FindFirstRecordByData("enroll_tokens", "token_hash", hash)
	if err != nil {
		return ErrTokenInvalid
	}

	if tk.GetString("used_at") == "" {
		// 没被用过却没抢到，只可能是过期。
		return ErrTokenExpired
	}

	machineID := tk.GetString("machine")
	if machineID == "" {
		return ErrTokenUsed
	}
	m, err := tx.FindRecordById("machines", machineID)
	if err != nil {
		return ErrTokenUsed
	}
	// 判据是公钥而非 token：偷到已用 token 的人没有对应的私钥，
	// 拿自己的公钥来重放会在这里被挡下。
	if m.GetString("pub_key") != pubKey {
		return ErrTokenUsed
	}

	*out = Result{
		MachineID:   m.Id,
		Fingerprint: m.GetString("fingerprint"),
		Replayed:    true,
	}
	return nil
}

// upsertMachine 按指纹建或更新机器记录，返回记录 id 与是否为 re-enroll。
func (s *Service) upsertMachine(tx core.App, req Request, fingerprint string) (string, bool, error) {
	existing, err := tx.FindFirstRecordByData("machines", "fingerprint", fingerprint)
	if err == nil {
		// 重装 agent 是常见操作，更新比拒绝友好。备注名是用户改过的，不动。
		applyMachineFields(existing, req)
		if err := tx.Save(existing); err != nil {
			return "", false, fmt.Errorf("enroll: 更新机器记录: %w", err)
		}
		return existing.Id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		// 真正的查询错误（比如库锁死）必须往上抛，不能当成「没这台机器」
		// 而去建一台重复的。
		return "", false, fmt.Errorf("enroll: 查询机器记录: %w", err)
	}

	c, err := tx.FindCollectionByNameOrId("machines")
	if err != nil {
		return "", false, fmt.Errorf("enroll: 找不到 machines collection: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("fingerprint", fingerprint)
	r.Set("name", req.Hostname) // 备注名默认取 hostname，用户可改
	r.Set("status", "offline")  // 还没建立 WS 连接
	applyMachineFields(r, req)
	if err := tx.Save(r); err != nil {
		return "", false, fmt.Errorf("enroll: 创建机器记录: %w", err)
	}
	return r.Id, false, nil
}

func applyMachineFields(r *core.Record, req Request) {
	r.Set("pub_key", req.PubKey)
	r.Set("hostname", req.Hostname)
	r.Set("os", req.OS)
	r.Set("arch", req.Arch)
	r.Set("agent_version", req.AgentVersion)
}
