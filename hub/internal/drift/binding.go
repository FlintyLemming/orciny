package drift

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/tidwall/gjson"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/importer"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

// baseURLToken 是基线里 claude 端点 base_url 那一处的字面形态。
//
// 只认 claude 端点（M1.6 spec §1.3）：漂移源只有 .claude/**，
// openai 端点本期不会产生漂移。
const baseURLToken = "{{provider.claude.base_url}}"

// DetectBindingDrift 识别绑定漂移（M1.5 spec §6.1）：
// **基线侧是 {{provider.base_url}} 占位符、现状侧是字面值**。
// 命中时返回现状侧那段字面 URL。
//
// 做法是按行比：基线里每一处占位符都落在某一行上，取它在这一行里的前缀
// 与后缀，去现状内容里找同前缀同后缀的行——中间那段就是机器上实际写的
// URL。按行而不是按字节偏移，是因为用户多半只改了那一行，其余行还对得上。
//
// 识别逻辑完全在 hub，agent 不需要知道「绑定」这个概念。
func DetectBindingDrift(base, cur []byte) (string, bool) {
	if len(cur) == 0 || !bytes.Contains(base, []byte(baseURLToken)) {
		return "", false
	}
	curLines := strings.Split(string(cur), "\n")

	for _, bl := range strings.Split(string(base), "\n") {
		i := strings.Index(bl, baseURLToken)
		if i < 0 {
			continue
		}
		trimmedBase := strings.TrimSpace(bl)
		prefix := strings.TrimLeft(bl[:i], " \t")

		for _, cl := range curLines {
			t := strings.TrimSpace(cl)
			if t == trimmedBase {
				continue // 这一行没被改，还是占位符
			}
			if !strings.HasPrefix(t, prefix) {
				continue
			}
			// 只按前缀锚定，值取到**紧邻的引号**为止，而不是要求整行后缀
			// 也对得上：紧凑写法的 settings.json 会把 base_url 与 key 放在
			// 同一行，而「换了家供应商」恰恰是两者一起改——要求整行后缀匹配
			// 会让最主要的场景漏报。
			mid := t[len(prefix):]
			if j := strings.IndexByte(mid, '"'); j >= 0 {
				mid = mid[:j]
			}
			mid = strings.Trim(mid, "\"' \t,")
			if mid == "" || strings.Contains(mid, "{{") {
				continue
			}
			// 不像 URL 就别乱报——用户可能只是把值清空了或写了句中文。
			if providers.HostOf(mid) == "" {
				continue
			}
			return mid, true
		}
	}
	return "", false
}

// BindingMatch 是一次绑定漂移的反查结果（M1.5 spec §6.3）。
type BindingMatch struct {
	// URL 是机器上那段字面 base_url。
	URL string `json:"url"`
	// Match 是三档判定的结果。Exact 为 false 时 UI 措辞降级为「可能是」。
	Match providers.Match `json:"match"`
	// KeyLocation / KeyMasked 是在漂移内容里找到的、机器上手写的那把 key。
	// 第二档的新建向导用它把 key 抽成凭据，用户不必再去平台后台复制一遍。
	KeyLocation string `json:"key_location,omitempty"`
	KeyMasked   string `json:"key_masked,omitempty"`
}

// MatchBinding 对一条绑定漂移做反查（M1.5 spec §6.3）。
//
// 这个交互的价值在于它匹配了真实用法：「我在某台机器上试了个新中转，好用」
// ——没有它，这个发现要手工搬回 Web 重做一遍；有了它，一键变成全机队的决定。
func (s *Service) MatchBinding(eventID string) (BindingMatch, error) {
	var out BindingMatch
	rec, err := s.d.App.FindRecordById("drift_events", eventID)
	if err != nil {
		return out, fmt.Errorf("drift: 漂移 %s 不存在: %w", eventID, err)
	}
	if !rec.GetBool("binding_drift") {
		return out, fmt.Errorf("drift: %s 不是绑定漂移", rec.GetString("path"))
	}
	out.URL = rec.GetString("binding_url")

	m, err := s.d.Providers.MatchBaseURL(out.URL)
	if err != nil {
		return out, err
	}
	out.Match = m

	if content, err := s.driftContent(rec); err == nil {
		out.KeyLocation, out.KeyMasked = findAuthKey(rec.GetString("path"), content)
	}
	return out, nil
}

// findAuthKey 在漂移内容里找那把机器上手写的 API key。
//
// 复用 importer 的敏感项检测（产品 §4.2 的既有能力），但只认 env 里两个
// 鉴权字段的位置——全文扫出来的其他候选与「换了家供应商」无关。
func findAuthKey(path string, content []byte) (location, masked string) {
	for _, f := range importer.Scan(path, content) {
		switch f.Location {
		case "env." + providers.AuthToken, "env." + providers.AuthAPIKey:
			return f.Location, f.Masked
		}
	}
	return "", ""
}

// ExtractKey 把漂移内容里 location 处的值抽成凭据，返回凭据记录 id。
//
// 源是漂移 blob 而不是草稿（与 importer.Extract 的区别就在这里）：
// 这把 key 是用户在**机器上**手写的，中台从没见过它。
func (s *Service) ExtractKey(eventID, location, credName string) (string, error) {
	rec, err := s.d.App.FindRecordById("drift_events", eventID)
	if err != nil {
		return "", fmt.Errorf("drift: 漂移 %s 不存在: %w", eventID, err)
	}
	content, err := s.driftContent(rec)
	if err != nil {
		return "", err
	}
	value := gjson.GetBytes(content, location).String()
	if value == "" {
		return "", fmt.Errorf("drift: %s 的 %s 取不到值", rec.GetString("path"), location)
	}
	// 长度下限由 credentials.Store.Create 把关（M1 spec §6.4）：
	// 太短的值抽成凭据会在还原时到处误匹配。
	cred, err := s.d.Creds.Create(credName, value,
		"从机器 "+rec.GetString("machine")+" 的漂移里抽取")
	if err != nil {
		return "", err
	}
	return cred.Id, nil
}

// driftContent 读一条漂移的现状内容。
func (s *Service) driftContent(rec *core.Record) ([]byte, error) {
	blobID := rec.GetString("current_blob")
	if blobID == "" {
		return nil, fmt.Errorf("drift: %s 没有现状内容", rec.GetString("path"))
	}
	b, err := s.d.App.FindRecordById("blobs", blobID)
	if err != nil {
		return nil, fmt.Errorf("drift: 读取现状内容: %w", err)
	}
	return s.d.Blobs.Get(b.GetString("hash"))
}

// Rebind 把漂移所属配置集的绑定改成指定 Provider，并发布一条新 Revision
// （M1.5 spec §2.2 / §6.3 第一档）。
//
// 发布的是 **head 的清单**而不是草稿：改绑定不该顺手把用户草稿里没做完的
// 编辑一起推上去。草稿绑定同步改掉，否则下一次发布会把绑定又切回去。
//
// **不在这里关掉这条漂移**：新 Revision 下发 → agent apply → ApplyAck →
// resolveDriftOnApply 会把它标成 superseded（M1 spec §7.7 的既有路径）。
// 抢先标记等于撒谎——那时机器上还没变。
func (s *Service) Rebind(eventID, providerID string) (*core.Record, error) {
	rec, err := s.d.App.FindRecordById("drift_events", eventID)
	if err != nil {
		return nil, fmt.Errorf("drift: 漂移 %s 不存在: %w", eventID, err)
	}
	if !rec.GetBool("binding_drift") {
		return nil, fmt.Errorf("drift: %s 不是绑定漂移", rec.GetString("path"))
	}
	setID := rec.GetString("config_set")
	if setID == "" {
		return nil, fmt.Errorf("drift: %s 不属于任何配置集", rec.GetString("path"))
	}

	prov, err := s.d.Providers.Get(providerID)
	if err != nil {
		return nil, err
	}
	// 模型槽取新 Provider 的 defaults：换了家供应商，旧供应商的模型 id
	// 在新 endpoint 上没有意义。
	var defaults providers.ModelSlots
	_ = prov.UnmarshalJSONField("defaults", &defaults)
	binding := &providers.Binding{Provider: providerID, Models: defaults}

	head, err := s.d.Revs.Head(setID)
	if err != nil {
		return nil, err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return nil, err
	}
	if err := s.d.Sets.SetDraftBinding(setID, binding); err != nil {
		return nil, err
	}
	rev, err := s.d.Revs.PublishFiles(setID, files, binding,
		"从收件箱改绑到「"+prov.GetString("name")+"」", "publish")
	if err != nil {
		return nil, err
	}
	if err := s.d.Events.Write(events.KindBindingChanged, rec.GetString("machine"),
		map[string]any{
			"config_set": setID, "provider": providerID,
			"revision": rev.Id, "from_drift": eventID,
		}); err != nil {
		s.log.Warn("写 binding.changed 事件失败", "error", err)
	}
	if s.d.Sync != nil {
		if err := s.d.Sync.NotifyConfigSet(setID, rev.Id, protocol.ReasonPublished); err != nil {
			return nil, err
		}
	}
	return rev, nil
}
