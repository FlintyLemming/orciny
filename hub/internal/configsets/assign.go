package configsets

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
)

var ErrNoAssignment = errors.New("configsets: 该机器未指派配置集")

// 指派状态与模式的取值（spec §4.1）。
const (
	ModeApply  = "apply"
	ModeSurvey = "survey"

	StatePending  = "pending"
	StateApplying = "applying"
	StateAligned  = "aligned"
	StateFailed   = "failed"
	StateDegraded = "degraded"
	StatePaused   = "paused"
)

// Assign 指派配置集给机器。一机一配置集由唯一索引强制，因此这里是 upsert。
//
// mode 必须由调用方显式给出：UI 上是「应用配置集」与「先看看」的强制二选一
// （spec §7.6），没有默认值可言——默认成 apply 会让第二台机器被静默覆盖。
func (s *Service) Assign(machineID, setID, mode string) (*core.Record, error) {
	if mode != ModeApply && mode != ModeSurvey {
		return nil, fmt.Errorf("configsets: mode 只能是 %s 或 %s，收到 %q", ModeApply, ModeSurvey, mode)
	}
	if _, err := s.record(setID); err != nil {
		return nil, err
	}

	r, err := s.Assignment(machineID)
	if errors.Is(err, ErrNoAssignment) {
		c, cerr := s.app.FindCollectionByNameOrId("assignments")
		if cerr != nil {
			return nil, fmt.Errorf("configsets: 找不到 collection: %w", cerr)
		}
		r = core.NewRecord(c)
		r.Set("machine", machineID)
	} else if err != nil {
		return nil, err
	}

	changed := r.GetString("config_set") != setID
	r.Set("config_set", setID)
	r.Set("mode", mode)
	if changed || r.GetString("state") == "" {
		// 换了配置集就回到 pending：旧的 applied_revision 不再有意义。
		r.Set("state", StatePending)
		r.Set("applied_revision", "")
		r.Set("last_error", "")
	}
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("configsets: 保存指派: %w", err)
	}
	if err := s.ev.Write(events.KindAssignChanged, machineID, map[string]any{
		"config_set": setID, "mode": mode,
	}); err != nil {
		s.app.Logger().Warn("写 assign.changed 事件失败", "error", err)
	}
	return r, nil
}

func (s *Service) Assignment(machineID string) (*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("assignments",
		"machine = {:m}", "", 1, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("configsets: 查询指派: %w", err)
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAssignment, machineID)
	}
	return recs[0], nil
}

func (s *Service) AssignedMachines(setID string) ([]string, error) {
	recs, err := s.app.FindRecordsByFilter("assignments",
		"config_set = {:s}", "", 0, 0, map[string]any{"s": setID})
	if err != nil {
		return nil, fmt.Errorf("configsets: 查询指派机器: %w", err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetString("machine"))
	}
	return out, nil
}
