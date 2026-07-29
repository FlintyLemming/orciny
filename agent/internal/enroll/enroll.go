// Package enroll 实现 agent 侧的注册流程（spec §4.4）。
//
// 顺序很重要：先生成密钥 → 再请求 hub → 校验（如给了 --hub-key）→ 最后钉扎。
// 校验不过就绝不写 hub.pub，否则等于把中间人认成了 hub。
package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/probe"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// ErrHubKeyMismatch 表示 hub 返回的公钥与 --hub-key 给出的指纹不符。
var ErrHubKeyMismatch = errors.New("enroll: hub 公钥指纹与 --hub-key 不符，已中止安装")

type Options struct {
	HubURL       string
	Token        string
	ExpectHubKey string
	IdentityDir  string
	AgentVersion string

	HTTPClient *http.Client
	Clock      clock.Clock
	Retries    int
	// RetryDelay 的 0 值有意义（测试里表示不等待），因此不做「0 → 默认」处理。
	RetryDelay time.Duration
}

type Outcome struct {
	MachineID   string
	Fingerprint string
	HubKeyFP    string
}

type request struct {
	Token        string `json:"token"`
	PubKey       string `json:"pubKey"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agentVersion"`
}

type response struct {
	HubPubKey   string `json:"hubPubKey"`
	MachineID   string `json:"machineId"`
	Fingerprint string `json:"fingerprint"`
}

// Run 执行一次 enroll。
func Run(ctx context.Context, o Options) (*Outcome, error) {
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Clock == nil {
		o.Clock = clock.System()
	}
	if o.Retries == 0 {
		o.Retries = 2
	}

	id, err := identity.LoadOrCreate(o.IdentityDir)
	if err != nil {
		return nil, err
	}

	hostname, goos, goarch := probe.Host()
	body, err := json.Marshal(request{
		Token:        o.Token,
		PubKey:       id.PublicKeyBase64(),
		Hostname:     hostname,
		OS:           goos,
		Arch:         goarch,
		AgentVersion: o.AgentVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("enroll: 构造请求: %w", err)
	}

	url := strings.TrimRight(o.HubURL, "/") + "/api/orciny/enroll"
	resp, err := postWithRetry(ctx, o, url, body)
	if err != nil {
		return nil, err
	}

	hubPub, err := protocol.DecodePublicKey(resp.HubPubKey)
	if err != nil {
		return nil, fmt.Errorf("enroll: hub 返回的公钥无法解析: %w", err)
	}
	hubFP := protocol.Fingerprint(hubPub)

	// 带外校验：--hub-key 给了就必须对上，否则中止且不钉扎。
	if o.ExpectHubKey != "" && o.ExpectHubKey != hubFP {
		return nil, fmt.Errorf("%w（期望 %s，实际 %s）", ErrHubKeyMismatch, o.ExpectHubKey, hubFP)
	}

	if err := identity.PinHubKey(o.IdentityDir, hubPub); err != nil {
		return nil, err
	}

	return &Outcome{
		MachineID:   resp.MachineID,
		Fingerprint: id.Fingerprint(), // 以本机公钥为准，不采信 hub 的说法
		HubKeyFP:    hubFP,
	}, nil
}

// postWithRetry 把瞬时网络问题挡在用户视线之外（spec §4.4）。
// 4xx 是终局错误，不重试——token 无效不会因为再试一次就变有效。
func postWithRetry(ctx context.Context, o Options, url string, body []byte) (*response, error) {
	var lastErr error
	for attempt := 0; attempt <= o.Retries; attempt++ {
		if attempt > 0 {
			timer := o.Clock.NewTimer(o.RetryDelay)
			select {
			case <-timer.C():
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		out, retryable, err := postOnce(ctx, o, url, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("enroll: 重试 %d 次后仍失败: %w", o.Retries, lastErr)
}

func postOnce(ctx context.Context, o Options, url string, body []byte) (*response, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("enroll: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("enroll: 连接 hub 失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var out response
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, false, fmt.Errorf("enroll: 解析响应: %w", err)
		}
		return &out, false, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// 注意：不把响应体原样打出来，它可能回显我们发过去的内容。
		return nil, false, fmt.Errorf("enroll: hub 拒绝了注册请求（HTTP %d）；"+
			"请确认 token 未过期、未被使用", resp.StatusCode)
	default:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, true, fmt.Errorf("enroll: hub 返回 HTTP %d", resp.StatusCode)
	}
}
