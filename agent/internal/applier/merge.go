package applier

import (
	"fmt"
	"os"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// mergeRetries 是竞写重试次数（spec §7.5）。
//
// Claude Code 自己也在写 ~/.claude.json（记录 project 状态等运行时数据），
// 而且它不用原子写。这里无法根治，只能缓解：读之前记 mtime + size，
// 写之前再查一次；变了就重读重试。
const mergeRetries = 3

// merge 只改受管键，其余字节原样保留。
//
// 用 sjson.SetRaw 做原地编辑而不是 Unmarshal 到 map 再 Marshal 回去：
// 后者会丢掉键顺序与格式，让用户每次打开文件都发现被整个重排了（spec §7.5）。
func (a *Applier) merge(abs string, s Step) error {
	for attempt := 0; attempt < mergeRetries; attempt++ {
		before, existed, err := a.statFingerprint(abs)
		if err != nil {
			return err
		}

		current := []byte("{}")
		if existed {
			current, err = a.fs.Read(abs)
			if err != nil {
				return fmt.Errorf("applier: 读取 %s: %w", s.Rel, err)
			}
		}
		if !gjson.ValidBytes(current) {
			// 磁盘上的 JSON 已经坏了就不能动它：sjson 会在一份坏结构上
			// 产出另一份坏结构，用户的 projects 记录就没了。
			return fmt.Errorf("applier: %s 不是合法 JSON，拒绝改写", s.Rel)
		}
		if !gjson.ValidBytes(s.Content) {
			return fmt.Errorf("applier: %s 的目标内容不是合法 JSON", s.Rel)
		}

		out := current
		for _, key := range s.Keys {
			want := gjson.GetBytes(s.Content, key)
			raw := "{}"
			if want.Exists() {
				raw = want.Raw
			}
			out, err = sjson.SetRawBytes(out, key, []byte(raw))
			if err != nil {
				return fmt.Errorf("applier: 合并 %s 的 %s: %w", s.Rel, key, err)
			}
		}

		if existed && string(out) == string(current) {
			return nil // 内容一致，不碰文件（幂等）
		}

		after, _, err := a.statFingerprint(abs)
		if err != nil {
			return err
		}
		if after != before {
			// 读到写之间文件被改过，重来。
			a.log.Debug("合并期间文件被改动，重试", "path", s.Rel, "attempt", attempt+1)
			continue
		}
		// rename 是原子的，写这一半没有中间态。
		return a.fs.Write(abs, out, s.Mode)
	}
	return fmt.Errorf("applier: %s 连续 %d 次在合并期间被改动，本次放弃"+
		"（下一次对账会重试）", s.Rel, mergeRetries)
}

// statFingerprint 返回 mtime+size 的指纹，用于检测竞写。
func (a *Applier) statFingerprint(abs string) (string, bool, error) {
	info, err := a.fs.Stat(abs)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("applier: stat %s: %w", abs, err)
	}
	return fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size()), true, nil
}
