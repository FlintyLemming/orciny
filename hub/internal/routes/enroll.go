package routes

import (
	"errors"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
)

type issueTokenResponse struct {
	Token          string `json:"token"`
	ExpiresAt      string `json:"expiresAt"`
	InstallCommand string `json:"installCommand"`
}

func (d Deps) issueToken(e *core.RequestEvent) error {
	token, expires, err := d.Enroll.IssueToken()
	if err != nil {
		e.App.Logger().Error("签发注册 token 失败", "error", err)
		return e.InternalServerError("签发注册 token 失败", nil)
	}

	base := baseURL(e)
	cmd := "curl -fsSL " + base + "/install.sh | sh -s -- --hub " + base +
		" --token " + token + " --hub-key " + d.Identity.Fingerprint()

	return e.JSON(http.StatusOK, issueTokenResponse{
		Token:          token,
		ExpiresAt:      expires.UTC().Format(time.RFC3339),
		InstallCommand: cmd,
	})
}

type enrollRequest struct {
	Token        string `json:"token"`
	PubKey       string `json:"pubKey"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agentVersion"`
}

type enrollResponse struct {
	HubPubKey   string `json:"hubPubKey"`
	MachineID   string `json:"machineId"`
	Fingerprint string `json:"fingerprint"`
}

func (d Deps) enroll(e *core.RequestEvent) error {
	var req enrollRequest
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}

	res, err := d.Enroll.Enroll(enroll.Request{
		Token:        req.Token,
		PubKey:       req.PubKey,
		Hostname:     req.Hostname,
		OS:           req.OS,
		Arch:         req.Arch,
		AgentVersion: req.AgentVersion,
	})
	switch {
	case err == nil:
	case errors.Is(err, enroll.ErrBadRequest), errors.Is(err, enroll.ErrBadPubKey):
		return e.BadRequestError("请求字段不完整或公钥格式错误", nil)
	case errors.Is(err, enroll.ErrTokenInvalid),
		errors.Is(err, enroll.ErrTokenExpired),
		errors.Is(err, enroll.ErrTokenUsed):
		// 三种情况对外统一成 401：这个端点不认证，不该变成 token 探测器。
		// 具体原因记在服务端日志里。
		e.App.Logger().Warn("enroll 被拒绝", "error", err, "hostname", req.Hostname)
		return e.UnauthorizedError("注册 token 无效、已过期或已被使用", nil)
	default:
		e.App.Logger().Error("enroll 失败", "error", err)
		return e.InternalServerError("注册失败", nil)
	}

	e.App.Logger().Info("机器已注册",
		"machine", res.MachineID,
		"fingerprint", res.Fingerprint,
		"re_enrolled", res.ReEnrolled,
		"replayed", res.Replayed,
	)

	return e.JSON(http.StatusOK, enrollResponse{
		HubPubKey:   d.Identity.PublicKeyBase64(),
		MachineID:   res.MachineID,
		Fingerprint: res.Fingerprint,
	})
}

type hubInfoResponse struct {
	Version     string `json:"version"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

func (d Deps) hubInfo(e *core.RequestEvent) error {
	return e.JSON(http.StatusOK, hubInfoResponse{
		Version:     d.Version,
		PublicKey:   d.Identity.PublicKeyBase64(),
		Fingerprint: d.Identity.Fingerprint(),
	})
}
