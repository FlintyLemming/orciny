package agent

import (
	"context"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny"
	internalenroll "github.com/FlintyLemming/orciny/agent/internal/enroll"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
)

// EnrollOptions 是 agent.Enroll 的入参。
type EnrollOptions struct {
	HubURL string
	Token  string
	// ExpectHubKey 对应 --hub-key，可空。给了就做带外校验。
	ExpectHubKey string
	// Dir 是 agent 目录（~/.orciny），不是 identity 子目录。
	Dir string
	// RetryDelay 为 0 表示重试之间不等待（测试用）。
	RetryDelay time.Duration
}

type EnrollResult struct {
	MachineID         string
	Fingerprint       string
	HubKeyFingerprint string
}

// Enroll 执行完整的注册流程并写好 agent.yml。
// CLI 与测试脚手架都走这个入口，保证两者行为一致。
func Enroll(ctx context.Context, o EnrollOptions) (*EnrollResult, error) {
	out, err := internalenroll.Run(ctx, internalenroll.Options{
		HubURL:       o.HubURL,
		Token:        o.Token,
		ExpectHubKey: o.ExpectHubKey,
		IdentityDir:  filepath.Join(o.Dir, identity.DirName),
		AgentVersion: orciny.Version,
		RetryDelay:   o.RetryDelay,
	})
	if err != nil {
		return nil, err
	}

	if err := SaveConfig(o.Dir, &Config{
		HubURL:    o.HubURL,
		MachineID: out.MachineID,
	}); err != nil {
		return nil, err
	}

	return &EnrollResult{
		MachineID:         out.MachineID,
		Fingerprint:       out.Fingerprint,
		HubKeyFingerprint: out.HubKeyFP,
	}, nil
}
