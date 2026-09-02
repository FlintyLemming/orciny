package drift

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/overrides"
	"github.com/FlintyLemming/orciny/internal/manifest"
)

var (
	// ErrPathUnmanaged：该路径在这台机器上已经退管，中台根本不下发它，
	// 覆盖层无处可盖（spec §2.3）。
	ErrPathUnmanaged = errors.New(
		"drift: 该路径已「不再受管」，请先在机器详情页恢复受管再做本机保留")

	// ErrNotOverridable：这条漂移压根没有可保留的差异点
	// ——二进制、未脱敏、整份删除，或 keys 模式的受管键之外（spec §8.2、§8.4）。
	ErrNotOverridable = errors.New("drift: 这条漂移不能做本机保留")

	// ErrNoPoints：前端显式给了空的勾选。多半是漏传，
	// 而不是用户想做一次空操作（spec §7）。
	ErrNoPoints = errors.New("drift: 没有勾选任何差异点")
)

// Selection 是一个 event 的勾选结果。零值（Given = false）= 全选，
// 这样 UI 的默认路径不必先算一遍差异点（spec §7）。
type Selection struct {
	Given     bool
	Selectors []string // json_key：被勾选的 selector
	Hunks     []int    // text：被勾选的 hunk 下标
}

// overridePlan 是一条漂移过完全部护栏之后、准备落库的形态。
type overridePlan struct {
	rec     *core.Record
	machine string
	path    string
	json    bool
	points  []overrides.Point // json_key
	base    []byte            // text
	mine    []byte            // text
}

// Override 把选中的漂移转成本机覆盖层（spec §4、§8）。
//
// 五条护栏全部在**任何写入之前**检查完：要么整批成，要么什么都不动
// ——与 AdoptReviewed 同一个规矩。
func (s *Service) Override(eventIDs []string, points map[string]Selection, reviewed []string) error {
	if len(eventIDs) == 0 {
		return fmt.Errorf("drift: 没有选中任何漂移")
	}
	if s.d.Overrides == nil {
		return fmt.Errorf("drift: 覆盖层服务未装配")
	}
	reviewedSet := map[string]bool{}
	for _, id := range reviewed {
		reviewedSet[id] = true
	}

	plans := make([]overridePlan, 0, len(eventIDs))
	for _, id := range eventIDs {
		p, err := s.planOverride(id, points[id], reviewedSet[id])
		if err != nil {
			return err
		}
		plans = append(plans, p)
	}

	now := types.NowDateTime()
	for _, p := range plans {
		if err := s.commitOverride(p, now); err != nil {
			return err
		}
	}

	// 立刻通知，不必等下一次拉取。不带 RevisionID：同版本、内容变了。
	if s.d.Sync == nil {
		return nil
	}
	notified := map[string]bool{}
	for _, p := range plans {
		if notified[p.machine] {
			continue
		}
		notified[p.machine] = true
		if err := s.d.Sync.NotifyOverride(p.machine); err != nil {
			s.log.Warn("建覆盖层后通知失败", "machine", p.machine, "error", err)
		}
	}
	return nil
}

// planOverride 过五条护栏并算出要落库的差异点。不写任何东西。
func (s *Service) planOverride(id string, sel Selection, reviewed bool) (overridePlan, error) {
	var p overridePlan

	rec, err := s.d.App.FindRecordById("drift_events", id)
	if err != nil {
		return p, fmt.Errorf("drift: 漂移 %s 不存在: %w", id, err)
	}
	if rec.GetString("state") != "open" {
		return p, fmt.Errorf("drift: 漂移 %s 已被处理过", id)
	}
	path := rec.GetString("path")
	machineID := rec.GetString("machine")

	// 护栏 1：绑定漂移（spec §8.2）。覆盖层会把 {{provider.*}} 拍平成
	// 硬编码字面值，绑定当场失效，且把 API key 明文写进 hub 库。
	if rec.GetBool("binding_drift") {
		return p, fmt.Errorf("%w：%s。请改用卡片上的「改绑定」，"+
			"或者「恢复」把它拉回基线", ErrBindingDrift, path)
	}
	// 护栏 2：未脱敏 / 整份删除（spec §8.2）。
	if rec.GetBool("truncated") {
		return p, fmt.Errorf("%w：%s 含未能安全脱敏的凭据", ErrNotOverridable, path)
	}
	if rec.GetString("kind") == "deleted" {
		return p, fmt.Errorf("%w：%s 在本机整份被删除，没有可保留的差异点；"+
			"想让这台机器不再收到它，请用「不再管这个路径」", ErrNotOverridable, path)
	}
	// 护栏 3：restore_partial 必须逐条看过（spec §8.2，同 adopt.go）。
	if rec.GetBool("restore_partial") && !reviewed {
		return p, fmt.Errorf("%w: %s", ErrNeedsReview, path)
	}
	// 护栏 4：与退管互斥（spec §2.3）。
	unmanaged, err := s.pathIsUnmanaged(machineID, path)
	if err != nil {
		return p, err
	}
	if unmanaged {
		return p, fmt.Errorf("%w：%s", ErrPathUnmanaged, path)
	}

	base, cur, err := s.overrideSides(rec)
	if err != nil {
		return p, err
	}
	if !isText(base) || !isText(cur) {
		return p, fmt.Errorf("%w：%s 是二进制内容", ErrNotOverridable, path)
	}

	p = overridePlan{rec: rec, machine: machineID, path: path}

	// kind 由文件内容决定：两侧都是 JSON 对象才走 json_key。
	if overrides.IsJSONObject(base) && overrides.IsJSONObject(cur) {
		p.json = true
		all, err := overrides.Split(base, cur)
		if err != nil {
			return p, fmt.Errorf("%w：%s 切分差异点失败: %v", ErrNotOverridable, path, err)
		}
		p.points, err = pickPoints(all, sel)
		if err != nil {
			return p, err
		}
		// 护栏 5：keys 模式的 selector 必须落在受管键内（spec §8.4）。
		if err := s.checkManagedKeys(machineID, path, p.points); err != nil {
			return p, err
		}
		return p, nil
	}

	keep, err := pickHunks(overrides.Hunks(base, cur), sel)
	if err != nil {
		return p, err
	}
	p.base = base
	p.mine = overrides.SynthesizeMine(base, cur, keep)
	return p, nil
}

// pickPoints 按勾选筛差异点。Given = false 时全选。
func pickPoints(all []overrides.Point, sel Selection) ([]overrides.Point, error) {
	if !sel.Given {
		if len(all) == 0 {
			return nil, fmt.Errorf("%w：这条漂移没有可切分的差异点", ErrNoPoints)
		}
		return all, nil
	}
	if len(sel.Selectors) == 0 {
		return nil, ErrNoPoints
	}
	want := map[string]bool{}
	for _, s := range sel.Selectors {
		want[s] = true
	}
	out := make([]overrides.Point, 0, len(sel.Selectors))
	for _, p := range all {
		if want[p.Selector] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w：勾选的 selector 都不在差异点里", ErrNoPoints)
	}
	return out, nil
}

// pickHunks 按勾选筛 hunk 下标。Given = false 时全选。
func pickHunks(hs []overrides.Hunk, sel Selection) ([]int, error) {
	if !sel.Given {
		if len(hs) == 0 {
			return nil, fmt.Errorf("%w：这条漂移没有可保留的改动块", ErrNoPoints)
		}
		out := make([]int, len(hs))
		for i := range hs {
			out[i] = i
		}
		return out, nil
	}
	if len(sel.Hunks) == 0 {
		return nil, ErrNoPoints
	}
	for _, i := range sel.Hunks {
		if i < 0 || i >= len(hs) {
			return nil, fmt.Errorf("%w：hunk 下标 %d 越界（共 %d 个）", ErrNoPoints, i, len(hs))
		}
	}
	return sel.Hunks, nil
}

// overrideSides 取这条漂移的基线侧与本机侧内容，两侧都在占位符空间。
func (s *Service) overrideSides(rec *core.Record) (base, cur []byte, err error) {
	if h := rec.GetString("base_hash"); h != "" {
		base, err = s.d.Blobs.Get(h)
		if err != nil {
			return nil, nil, fmt.Errorf("drift: 取 %s 的基线内容: %w",
				rec.GetString("path"), err)
		}
	}
	blobID := rec.GetString("current_blob")
	if blobID == "" {
		return nil, nil, fmt.Errorf("%w：%s 的本机内容不在库里",
			ErrNotOverridable, rec.GetString("path"))
	}
	b, err := s.d.App.FindRecordById("blobs", blobID)
	if err != nil {
		return nil, nil, fmt.Errorf("drift: %s 的内容不在库里: %w",
			rec.GetString("path"), err)
	}
	cur, err = s.d.Blobs.Get(b.GetString("hash"))
	if err != nil {
		return nil, nil, err
	}
	return base, cur, nil
}

// pathIsUnmanaged 判断该路径在这台机器上是否已退管（机器级或全局规则）。
func (s *Service) pathIsUnmanaged(machineID, path string) (bool, error) {
	rules, err := s.IgnorePaths(machineID)
	if err != nil {
		return false, err
	}
	for _, p := range rules {
		if manifest.MatchGlob(p, path) || p == path {
			return true, nil
		}
	}
	return false, nil
}

// checkManagedKeys 拦下 keys 模式受管键之外的 selector（spec §8.4）。
//
// agent 的 applier.merge 只把 f.Keys 列出的键合进磁盘文件，受管键之外
// 中台从来不写。在那里建覆盖层毫无作用——用户会得到一个
// 「设置好了但什么都没发生」的状态。
func (s *Service) checkManagedKeys(machineID, path string, points []overrides.Point) error {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		return err
	}
	head, err := s.d.Revs.Head(assign.GetString("config_set"))
	if err != nil {
		return err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return err
	}
	var keys []string
	for _, f := range files {
		if f.Path == path {
			keys = f.Keys
			break
		}
	}
	if len(keys) == 0 {
		return nil // 不是 keys 模式，整份文件都受管
	}
	for _, p := range points {
		ok := false
		for _, k := range keys {
			esc := overrides.EscapeSeg(k)
			if p.Selector == esc || len(p.Selector) > len(esc) &&
				p.Selector[:len(esc)+1] == esc+"." {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%w：%s 是「只管指定键」模式（受管键 %v），"+
				"而 %s 在受管键之外——中台从来不写那里，建了也不会生效",
				ErrNotOverridable, path, keys, p.Selector)
		}
	}
	return nil
}

// commitOverride 落库：先按「新的替换旧的」清场，再建记录、置 overridden。
func (s *Service) commitOverride(p overridePlan, now types.DateTime) error {
	// kind 变了（用户把 JSON 文件改成了非 JSON，或反之）→ 整条路径重来。
	existing, err := s.d.Overrides.ForPath(p.machine, p.path)
	if err != nil {
		return err
	}
	wantKind := "text"
	if p.json {
		wantKind = "json_key"
	}
	kindChanged := len(existing) > 0 && existing[0].GetString("kind") != wantKind
	if kindChanged || !p.json {
		// 文本一个路径只有一条记录，同样是整条路径替换。
		n, err := s.d.Overrides.DropPath(p.machine, p.path)
		if err != nil {
			return err
		}
		s.writeReplaced(p, n)
	} else {
		total := 0
		for _, pt := range p.points {
			n, err := s.d.Overrides.DropOverlapping(p.machine, p.path, pt.Selector)
			if err != nil {
				return err
			}
			total += n
		}
		s.writeReplaced(p, total)
	}

	if p.json {
		if err := s.d.Overrides.CreateJSON(p.machine, p.path, p.rec.Id, p.points); err != nil {
			return err
		}
	} else if err := s.d.Overrides.CreateText(p.machine, p.path, p.rec.Id, p.base, p.mine); err != nil {
		return err
	}

	p.rec.Set("state", "overridden")
	p.rec.Set("resolved_at", now)
	if err := s.d.App.Save(p.rec); err != nil {
		return fmt.Errorf("drift: 标记 %s 已转覆盖层: %w", p.path, err)
	}
	detail := map[string]any{"path": p.path, "kind": kindOf(p.json)}
	if p.json {
		detail["points"] = len(p.points)
	}
	if err := s.d.Events.Write(events.KindOverrideCreated, p.machine, detail); err != nil {
		s.log.Warn("写 override.created 事件失败", "error", err)
	}
	return nil
}

func (s *Service) writeReplaced(p overridePlan, n int) {
	if n == 0 {
		return
	}
	// 重叠会让合并结果依赖应用顺序——不可接受。用户最后点的那次
	// 就是他的意思（spec §8.5）。
	if err := s.d.Events.Write(events.KindOverrideReplaced, p.machine, map[string]any{
		"path": p.path, "replaced": n,
	}); err != nil {
		s.log.Warn("写 override.replaced 事件失败", "error", err)
	}
}

func kindOf(isJSON bool) string {
	if isJSON {
		return "json_key"
	}
	return "text"
}
