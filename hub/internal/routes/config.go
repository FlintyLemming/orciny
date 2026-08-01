package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/importer"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Admin 是路由层需要的管理动作，由 *hub.Hub 实现。
// 放接口是为了避免 routes import hub 成环。
type Admin interface {
	AssignConfigSet(machineID, setID, mode string) error
	PublishConfigSet(setID, note string) (string, error)
	RollbackConfigSet(setID, revisionID string) (string, error)
	CreateCredential(name, value, note string) error
	RotateCredential(name, value string) error
	DeleteCredential(name string) error
	StartImport(machineID string) (string, string, error)
	CloneConfigSet(setID, newName string) (string, error)
	DeleteConfigSet(setID string) error
	ImportFindings(setID string) ([]importer.Finding, error)
	ExtractCredential(setID, path, location, name string) error
	AdoptDrift(eventIDs []string) (string, error)
	AdoptDriftReviewed(eventIDs, reviewed []string) (string, error)
	RestoreDrift(eventIDs []string) error
	IgnoreDrift(eventIDs []string, global bool) error
	ClearDegraded(machineID string) error
}

// ---------- config sets ----------

func (d Deps) createConfigSet(e *core.RequestEvent) error {
	if d.Sets == nil {
		return e.InternalServerError("配置集服务未就绪", nil)
	}
	var req struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if err := e.BindBody(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		return e.BadRequestError("需要 name", nil)
	}
	rec, err := d.Sets.Create(req.Name, req.Note)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{
		"id": rec.Id, "name": rec.GetString("name"), "note": rec.GetString("note"),
	})
}

func (d Deps) cloneConfigSet(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := e.BindBody(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		return e.BadRequestError("需要 name", nil)
	}
	id, err := d.Admin.CloneConfigSet(e.Request.PathValue("id"), req.Name)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"id": id})
}

func (d Deps) deleteConfigSet(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	if err := d.Admin.DeleteConfigSet(e.Request.PathValue("id")); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) setDraftFile(e *core.RequestEvent) error {
	if d.Sets == nil {
		return e.InternalServerError("配置集服务未就绪", nil)
	}
	var req struct {
		Path    string   `json:"path"`
		Content string   `json:"content"`
		Mode    uint32   `json:"mode"`
		Keys    []string `json:"keys"`
	}
	if err := e.BindBody(&req); err != nil || req.Path == "" {
		return e.BadRequestError("需要 path", nil)
	}
	if req.Mode == 0 {
		req.Mode = 0o644
	}
	entry, err := d.Sets.SetDraftFile(e.Request.PathValue("id"), req.Path, []byte(req.Content), req.Mode, req.Keys)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, fileEntryJSON(entry))
}

func (d Deps) removeDraftFile(e *core.RequestEvent) error {
	if d.Sets == nil {
		return e.InternalServerError("配置集服务未就绪", nil)
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := e.BindBody(&req); err != nil || req.Path == "" {
		return e.BadRequestError("需要 path", nil)
	}
	if err := d.Sets.RemoveDraftFile(e.Request.PathValue("id"), req.Path); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) setManifest(e *core.RequestEvent) error {
	if d.Sets == nil {
		return e.InternalServerError("配置集服务未就绪", nil)
	}
	var m manifest.Manifest
	if err := e.BindBody(&m); err != nil {
		return e.BadRequestError("manifest JSON 格式错误", nil)
	}
	if err := d.Sets.SetManifest(e.Request.PathValue("id"), m); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) validateConfigSet(e *core.RequestEvent) error {
	if d.Sets == nil || d.Creds == nil {
		return e.InternalServerError("配置集服务未就绪", nil)
	}
	known, err := d.knownRefs(e.App)
	if err != nil {
		return mapErr(e, err)
	}
	problems, err := d.Sets.Validate(e.Request.PathValue("id"), known)
	if err != nil {
		return mapErr(e, err)
	}
	if problems == nil {
		problems = []configsets.Problem{}
	}
	return e.JSON(http.StatusOK, problems)
}

func (d Deps) publishConfigSet(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = e.BindBody(&req)
	revID, err := d.Admin.PublishConfigSet(e.Request.PathValue("id"), req.Note)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"revision": revID})
}

func (d Deps) rollbackConfigSet(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Revision string `json:"revision"`
	}
	if err := e.BindBody(&req); err != nil || req.Revision == "" {
		return e.BadRequestError("需要 revision", nil)
	}
	revID, err := d.Admin.RollbackConfigSet(e.Request.PathValue("id"), req.Revision)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"revision": revID})
}

func (d Deps) diffConfigSet(e *core.RequestEvent) error {
	if d.Sets == nil || d.Revs == nil || d.Blobs == nil {
		return e.InternalServerError("配置集服务未就绪", nil)
	}
	setID := e.Request.PathValue("id")
	fromRef := e.Request.URL.Query().Get("from")
	toRef := e.Request.URL.Query().Get("to")
	if fromRef == "" || toRef == "" {
		return e.BadRequestError("需要 from 与 to 查询参数", nil)
	}

	from, err := d.resolveFiles(setID, fromRef)
	if err != nil {
		return mapErr(e, err)
	}
	to, err := d.resolveFiles(setID, toRef)
	if err != nil {
		return mapErr(e, err)
	}

	changes := revisions.Diff(from, to)
	if changes == nil {
		changes = []revisions.FileChange{}
	}
	diffs := map[string]string{}
	for _, c := range changes {
		if c.Kind == "added" || c.Kind == "removed" || c.Kind == "modified" {
			var a, b []byte
			if c.FromHash != "" {
				a, _ = d.Blobs.Get(c.FromHash)
			}
			if c.ToHash != "" {
				b, _ = d.Blobs.Get(c.ToHash)
			}
			diffs[c.Path] = unifiedDiff(c.Path, a, b)
		}
	}
	return e.JSON(http.StatusOK, map[string]any{"changes": changes, "diffs": diffs})
}

func (d Deps) resolveFiles(setID, ref string) ([]protocol.FileEntry, error) {
	if ref == "draft" {
		return d.Sets.Draft(setID)
	}
	return d.Revs.Files(ref)
}

func (d Deps) getBlob(e *core.RequestEvent) error {
	if d.Blobs == nil {
		return e.InternalServerError("blob 服务未就绪", nil)
	}
	content, err := d.Blobs.Get(e.Request.PathValue("hash"))
	if err != nil {
		return mapErr(e, err)
	}
	e.Response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	return e.String(http.StatusOK, string(content))
}

// ---------- assignments ----------

func (d Deps) assign(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Machine   string `json:"machine"`
		ConfigSet string `json:"config_set"`
		Mode      string `json:"mode"`
	}
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	if req.Machine == "" || req.ConfigSet == "" {
		return e.BadRequestError("需要 machine 与 config_set", nil)
	}
	if req.Mode != "apply" && req.Mode != "survey" {
		return e.BadRequestError("mode 必须是 apply 或 survey", nil)
	}
	if err := d.Admin.AssignConfigSet(req.Machine, req.ConfigSet, req.Mode); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------- credentials ----------

func (d Deps) createCredential(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Note  string `json:"note"`
	}
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	if err := d.Admin.CreateCredential(req.Name, req.Value, req.Note); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) rotateCredential(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	name, err := d.credName(e, e.Request.PathValue("id"))
	if err != nil {
		return mapErr(e, err)
	}
	if err := d.Admin.RotateCredential(name, req.Value); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) deleteCredential(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	name, err := d.credName(e, e.Request.PathValue("id"))
	if err != nil {
		return mapErr(e, err)
	}
	if err := d.Admin.DeleteCredential(name); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// credName 接受 id 或 name。
func (d Deps) credName(e *core.RequestEvent, idOrName string) (string, error) {
	if d.Creds != nil {
		if r, err := e.App.FindRecordById("credentials", idOrName); err == nil && r != nil {
			return r.GetString("name"), nil
		}
	}
	return idOrName, nil
}

// ---------- variables ----------

func (d Deps) setVariables(e *core.RequestEvent) error {
	if d.Creds == nil {
		return e.InternalServerError("凭据服务未就绪", nil)
	}
	machineID := e.Request.PathValue("id")
	var req map[string]string
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	for k, v := range req {
		if err := d.Creds.SetVariable(machineID, k, v); err != nil {
			return mapErr(e, err)
		}
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------- import ----------

func (d Deps) startImport(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	token, setID, err := d.Admin.StartImport(e.Request.PathValue("id"))
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"token": token, "config_set": setID})
}

func (d Deps) extractCredential(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Path     string `json:"path"`
		Location string `json:"location"`
		Name     string `json:"name"`
	}
	if err := e.BindBody(&req); err != nil || req.Path == "" || req.Location == "" || req.Name == "" {
		return e.BadRequestError("需要 path、location、name", nil)
	}
	if err := d.Admin.ExtractCredential(e.Request.PathValue("id"), req.Path, req.Location, req.Name); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) importFindings(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	findings, err := d.Admin.ImportFindings(e.Request.PathValue("id"))
	if err != nil {
		return mapErr(e, err)
	}
	if findings == nil {
		findings = []importer.Finding{}
	}
	return e.JSON(http.StatusOK, findings)
}

// ---------- drift inbox ----------

func (d Deps) adoptDrift(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Events   []string `json:"events"`
		Reviewed []string `json:"reviewed"`
	}
	if err := e.BindBody(&req); err != nil || len(req.Events) == 0 {
		return e.BadRequestError("需要 events", nil)
	}
	var (
		revID string
		err   error
	)
	if len(req.Reviewed) > 0 {
		revID, err = d.Admin.AdoptDriftReviewed(req.Events, req.Reviewed)
	} else {
		revID, err = d.Admin.AdoptDrift(req.Events)
	}
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"revision": revID})
}

func (d Deps) restoreDrift(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Events []string `json:"events"`
	}
	if err := e.BindBody(&req); err != nil || len(req.Events) == 0 {
		return e.BadRequestError("需要 events", nil)
	}
	if err := d.Admin.RestoreDrift(req.Events); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) ignoreDrift(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Events []string `json:"events"`
		Global bool     `json:"global"`
	}
	if err := e.BindBody(&req); err != nil || len(req.Events) == 0 {
		return e.BadRequestError("需要 events", nil)
	}
	if err := d.Admin.IgnoreDrift(req.Events, req.Global); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) clearDegraded(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	id := e.Request.PathValue("id")
	if id == "" {
		return e.BadRequestError("需要机器 id", nil)
	}
	if err := d.Admin.ClearDegraded(id); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// ---------- helpers ----------

func (d Deps) knownRefs(app core.App) (map[string]bool, error) {
	known := map[string]bool{}
	creds, err := app.FindAllRecords("credentials")
	if err != nil {
		return nil, err
	}
	for _, r := range creds {
		known["cred."+r.GetString("name")] = true
	}
	vars, err := app.FindAllRecords("variables")
	if err != nil {
		return nil, err
	}
	for _, r := range vars {
		known["var."+r.GetString("key")] = true
	}
	return known, nil
}

func fileEntryJSON(f protocol.FileEntry) map[string]any {
	return map[string]any{
		"path": f.Path,
		"hash": f.Hash,
		"size": f.Size,
		"mode": f.Mode,
		"keys": f.Keys,
	}
}

func unifiedDiff(path string, a, b []byte) string {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(a)),
		B:        difflib.SplitLines(string(b)),
		FromFile: "a/" + path,
		ToFile:   "b/" + path,
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return ""
	}
	return text
}

// mapErr 把业务错误映射成合适的 HTTP 状态码。
func mapErr(e *core.RequestEvent, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, credentials.ErrInUse):
		return e.JSON(http.StatusConflict, map[string]any{
			"message": err.Error(),
			"data":    map[string]any{},
		})
	case errors.Is(err, drift.ErrConflict):
		// 跨机器同路径冲突：409 + 冲突路径（错误串里带 path）。
		return e.JSON(http.StatusConflict, map[string]any{
			"message": err.Error(),
			"data":    map[string]any{"reason": "conflict"},
		})
	case errors.Is(err, drift.ErrNeedsReview):
		return e.JSON(http.StatusConflict, map[string]any{
			"message": err.Error(),
			"data":    map[string]any{"reason": "needs_review"},
		})
	case errors.Is(err, drift.ErrMixedConfigSets):
		return e.BadRequestError(err.Error(), nil)
	case errors.Is(err, credentials.ErrShortValue),
		errors.Is(err, credentials.ErrBadName):
		return e.BadRequestError(err.Error(), nil)
	case errors.Is(err, credentials.ErrNotFound),
		errors.Is(err, blobs.ErrNotFound),
		errors.Is(err, configsets.ErrNoAssignment):
		return e.NotFoundError(err.Error(), nil)
	case errors.Is(err, machines.ErrOffline):
		return e.JSON(http.StatusServiceUnavailable, map[string]any{
			"message": "机器不在线，请稍后再试",
			"data":    map[string]any{},
		})
	case isNotFound(err):
		return e.NotFoundError(err.Error(), nil)
	case isBadRequest(err):
		return e.BadRequestError(err.Error(), nil)
	default:
		e.App.Logger().Error("管理 API 失败", "error", err)
		return e.InternalServerError("内部错误", nil)
	}
}

func isNotFound(err error) bool {
	s := err.Error()
	return strings.Contains(s, "不存在") || strings.Contains(s, "not found")
}

func isBadRequest(err error) bool {
	s := err.Error()
	return strings.Contains(s, "非法") ||
		strings.Contains(s, "不可纳管") ||
		strings.Contains(s, "超过") ||
		strings.Contains(s, "mode") ||
		strings.Contains(s, "格式") ||
		strings.Contains(s, "未能安全脱敏") ||
		strings.Contains(s, "没有选中") ||
		strings.Contains(s, "已被处理过")
}
