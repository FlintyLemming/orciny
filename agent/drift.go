package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/agent/internal/watcher"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// LocalDrift 在本机做一次全量对账，不联网、不起监视。
//
// 没有 state.json 时返回空切片（还没 apply 过，谈不上漂移）。
func LocalDrift(dir string) ([]protocol.DriftItem, error) {
	st, err := state.Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	home, err := cfg.ManagedHomeDir()
	if err != nil {
		return nil, err
	}
	sec, err := secrets.Load(dir)
	if err != nil {
		return nil, err
	}
	m, err := manifestOf(st)
	if err != nil {
		return nil, err
	}

	w, err := watcher.New(watcher.Options{
		Dir:         dir,
		ManagedHome: home,
		Clock:       clock.System(),
		Report:      nil, // 不联网
	})
	if err != nil {
		return nil, err
	}
	if err := w.Reload(st, m, sec); err != nil {
		return nil, err
	}
	return w.Scan(true)
}

func manifestOf(st *state.State) (manifest.Manifest, error) {
	if len(st.Manifest) == 0 {
		return manifest.Default(), nil
	}
	return manifest.Parse(st.Manifest)
}

// SetPaused 写 state.json 的 paused 标志。
//
// 从没 apply 过时建一份最小 state，而不是报错——pause 是本地开关，
// 落盘即生效；上报 hub 是附加的（由下一次握手的 MachineInfo.LocalPaused 带上）。
func SetPaused(dir string, paused bool) error {
	st, err := state.Load(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		st = &state.State{
			Files:  map[string]state.FileState{},
			Health: state.HealthOK,
		}
	}
	st.Paused = paused
	return state.Save(dir, st)
}

// ensureManifestJSON 给测试与 seed 用：把 Default manifest 冻成 JSON。
func ensureManifestJSON() json.RawMessage {
	b, err := manifest.Default().JSON()
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

// kindName 把 DriftItem.Kind 翻成人话。
func kindName(k uint8) string {
	switch k {
	case protocol.DriftAdded:
		return "added"
	case protocol.DriftModified:
		return "modified"
	case protocol.DriftDeleted:
		return "deleted"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}
