// Package secrets 读写 ~/.orciny/secrets.json（0600，spec §6.2）。
//
// 这里存的是**明文**的凭据与变量值。理由不是图省事，是三个功能的前提：
//  1. 漂移 diff 不泄密：上报前要把磁盘里的真实值替回 {{cred.x}}，得有值才能替
//  2. hub 离线时仍能自愈：重渲染基线、恢复、回滚都不必等 hub
//  3. 对账不依赖网络：5 分钟一次的对账若每次都要向 hub 换凭据，hub 一断就瞎
//
// 安全上这不增加实质暴露面：这些值本来就以明文躺在同一台机器、同一个用户、
// 同样权限的 ~/.claude/settings.json 里。真正的边界是「机器被攻陷」，
// 而那时 settings.json 已经先失守了。
package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/protocol"
)

const FileName = "secrets.json"

type File struct {
	Creds   map[string]string `json:"creds"`
	Vars    map[string]string `json:"vars"`
	Machine map[string]string `json:"machine"`
	// Provider 是服务绑定注入的六个内置名（M1.5 spec §3.1）。
	// auth_token 是秘密，因此本文件仍然一律 0600。
	Provider map[string]string `json:"provider"`
}

func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load 读回缓存。文件不存在返回空 File 而不是错误——首次运行的常态。
func Load(dir string) (*File, error) {
	b, err := os.ReadFile(Path(dir))
	if os.IsNotExist(err) {
		return &File{
			Creds: map[string]string{}, Vars: map[string]string{},
			Machine: map[string]string{}, Provider: map[string]string{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: 读取: %w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("secrets: %s 损坏: %w", FileName, err)
	}
	if f.Creds == nil {
		f.Creds = map[string]string{}
	}
	if f.Vars == nil {
		f.Vars = map[string]string{}
	}
	if f.Machine == nil {
		f.Machine = map[string]string{}
	}
	if f.Provider == nil {
		f.Provider = map[string]string{}
	}
	return &f, nil
}

func Save(dir string, f *File) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: 序列化: %w", err)
	}
	return atomicfile.Write(Path(dir), b, 0o600)
}

// Lookup 是渲染时的取值函数，直接喂给 protocol.Render。
func (f *File) Lookup(r protocol.Ref) (string, bool) {
	if f == nil {
		return "", false
	}
	switch r.Kind {
	case protocol.RefCred:
		v, ok := f.Creds[r.Name]
		return v, ok
	case protocol.RefVar:
		v, ok := f.Vars[r.Name]
		return v, ok
	case protocol.RefMachine:
		v, ok := f.Machine[r.Name]
		return v, ok
	case protocol.RefProvider:
		v, ok := f.Provider[r.Name]
		return v, ok
	default:
		return "", false
	}
}

// Equal 用于检测凭据轮换：拉回来的快照 revision 没变但 secrets 变了，
// 就只重渲染受影响的文件（spec §5.1）。
func (f *File) Equal(o *File) bool {
	if f == nil || o == nil {
		return f == nil && o == nil
	}
	return sameMap(f.Creds, o.Creds) && sameMap(f.Vars, o.Vars) &&
		sameMap(f.Machine, o.Machine) && sameMap(f.Provider, o.Provider)
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
