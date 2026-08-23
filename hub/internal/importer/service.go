package importer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/tidwall/gjson"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Sender 与 configsync.Sender 同形。这里重新声明一遍是为了不让 importer
// 依赖 configsync——两者是平级的编排者，没有上下游关系。
type Sender interface {
	SendTo(machineID string, kind protocol.Kind, payload any) error
	Online(machineID string) bool
}

type Service struct {
	app    core.App
	blobs  *blobs.Store
	sets   *configsets.Service
	provs  *providers.Store
	ev     *events.Writer
	sender Sender

	// 进行中的采集：machineID → {token, setID}。
	// 不落库：一次采集只跨几秒钟，hub 重启后重来一次即可，
	// 而多一张表就多一份要清理的垃圾。
	pending map[string]session
}

type session struct {
	token string
	setID string
}

func NewService(app core.App, b *blobs.Store, sets *configsets.Service,
	provs *providers.Store, ev *events.Writer, sender Sender) *Service {
	return &Service{
		app: app, blobs: b, sets: sets, provs: provs, ev: ev, sender: sender,
		pending: map[string]session{},
	}
}

// Start 触发一次采集，同时建好草稿态配置集。
//
// 不需要单独的 import_sessions collection——草稿本来就是这个形状（spec §9.1）。
func (s *Service) Start(machineID string) (string, string, error) {
	m, err := s.app.FindRecordById("machines", machineID)
	if err != nil {
		return "", "", fmt.Errorf("importer: 机器 %s 不存在: %w", machineID, err)
	}
	name := m.GetString("name")
	if name == "" {
		name = m.GetString("hostname")
	}
	if name == "" {
		name = "导入的配置集"
	}

	set, err := s.sets.Create(uniqueName(s.app, name+" 的配置"), "从 "+name+" 采集")
	if err != nil {
		return "", "", err
	}

	mj, err := manifest.Default().JSON()
	if err != nil {
		return "", "", err
	}
	token, err := newToken()
	if err != nil {
		return "", "", err
	}
	s.pending[machineID] = session{token: token, setID: set.Id}

	if err := s.sender.SendTo(machineID, protocol.KindCollectRequest,
		protocol.CollectRequest{Manifest: mj, Token: token}); err != nil {
		return "", "", fmt.Errorf("importer: 机器不在线，无法采集: %w", err)
	}
	return token, set.Id, nil
}

// HandleResult 落一批采集结果。分批到达，Final 之后收尾。
func (s *Service) HandleResult(machineID string, res protocol.CollectResult) error {
	sess, ok := s.pending[machineID]
	if !ok {
		return fmt.Errorf("importer: 机器 %s 没有进行中的采集", machineID)
	}
	// token 随结果回传，防串批（spec §5.2）：用户连点两次「采集」时，
	// 第一批的结果不该落进第二次的草稿。
	if res.Token != sess.token {
		return fmt.Errorf("importer: 采集批次不匹配，已丢弃")
	}
	if res.Error != "" {
		delete(s.pending, machineID)
		return fmt.Errorf("importer: agent 采集失败: %s", res.Error)
	}

	for _, f := range res.Files {
		// 恒排除在 hub 侧再判一次（spec §3.2 的双侧纵深防御）。
		if manifest.IsAlwaysExcluded(f.Path) {
			s.app.Logger().Warn("采集结果里出现恒排除路径，已丢弃",
				"machine", machineID, "path", f.Path)
			continue
		}
		// 采集到的是**原始内容**，其中字面的 "{{" 必须转义，
		// 否则用户 CLAUDE.md 里讲模板语法的那段会被当成占位符（spec §6.1）。
		escaped := []byte(protocol.EscapeLiteral(string(f.Content)))
		if _, err := s.sets.SetDraftFile(sess.setID, f.Path, escaped, f.Mode, keysFor(f.Path)); err != nil {
			s.app.Logger().Warn("落草稿失败", "path", f.Path, "error", err)
		}
	}

	if !res.Final {
		return nil
	}
	delete(s.pending, machineID)

	detail := map[string]any{"config_set": sess.setID, "skipped": len(res.Skipped)}
	if len(res.Skipped) > 0 {
		var paths []string
		for _, sk := range res.Skipped {
			paths = append(paths, sk.Path+"("+sk.Reason+")")
		}
		detail["skipped_paths"] = paths
	}
	if err := s.ev.Write(events.KindImportCompleted, machineID, detail); err != nil {
		s.app.Logger().Warn("写 import.completed 事件失败", "error", err)
	}
	return nil
}

// Findings 对草稿的全部内容跑一遍敏感项检测。
func (s *Service) Findings(setID string) ([]Finding, error) {
	draft, err := s.sets.Draft(setID)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, f := range draft {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			continue
		}
		out = append(out, Scan(f.Path, content)...)
	}
	return out, nil
}

// Extract 把某处的值抽成 provider 某个端点的 key：写进 provider →
// 把该文件里**每一处**该值替成该端点的 key 占位符 → 重写草稿。
//
// 「每一处」是要紧的（M1 spec §1.4）：同一个 key 常常同时出现在 env 与
// 某段说明文字里，漏一处就等于没脱敏，而 Revision 是不可变的，
// 写进去就洗不掉。
//
// 与 M1 的区别只在去处：值从「建一条凭据」改成「写进 provider 的端点 key」
// （M1.6 spec §5.5）。凭据实体提供的加密落库、末四位回显 provider 自己完全
// 能承担；而「起个名再回来选它」这一步的心智负担被去掉了。
func (s *Service) Extract(setID, path, location, providerID, endpoint string) error {
	token, err := keyTokenOf(endpoint)
	if err != nil {
		return err
	}
	prov, err := s.provs.Get(providerID)
	if err != nil {
		return err
	}

	draft, err := s.sets.Draft(setID)
	if err != nil {
		return err
	}
	var entry *protocol.FileEntry
	for i := range draft {
		if draft[i].Path == path {
			entry = &draft[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("importer: 草稿里没有 %s", path)
	}
	content, err := s.blobs.Get(entry.Hash)
	if err != nil {
		return err
	}

	value := gjson.GetBytes(content, location).String()
	if value == "" {
		return fmt.Errorf("importer: %s 的 %s 取不到值", path, location)
	}
	if len(value) < secretbox.MinValueLen {
		return fmt.Errorf("importer: %s 的值只有 %d 个字符，短于 %d，"+
			"抽出来之后还原时会到处误匹配；请保留明文或改用机器变量",
			location, len(value), secretbox.MinValueLen)
	}

	if err := s.provs.SetEndpointKey(prov, endpoint, value); err != nil {
		return err
	}

	replaced := strings.ReplaceAll(string(content), value, token)
	if _, err := s.sets.SetDraftFile(setID, path, []byte(replaced), entry.Mode, entry.Keys); err != nil {
		return err
	}
	return nil
}

// keyTokenOf 给出某端点承载 key 的占位符字面形态。
// 两侧的 key 名不同（claude.auth_token vs openai.api_key，spec §3.1），
// 不能拿一个常量套两边。
func keyTokenOf(endpoint string) (string, error) {
	switch endpoint {
	case providers.EndpointClaude:
		return "{{provider.claude.auth_token}}", nil
	case providers.EndpointOpenAI:
		return "{{provider.openai.api_key}}", nil
	default:
		return "", fmt.Errorf("importer: 未知端点 %q", endpoint)
	}
}

// MatchProviderIn 对文件里的 base_url 做一次反查，供抽取向导预选 provider
// （M1.6 spec §5.5 第 2 步）。找不到 base_url 或反查不中都返回 MatchNone，
// 不是错误——那只是「让用户自己选」。
func (s *Service) MatchProviderIn(setID, path string) (providers.Match, error) {
	draft, err := s.sets.Draft(setID)
	if err != nil {
		return providers.Match{}, err
	}
	for _, f := range draft {
		if f.Path != path {
			continue
		}
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			return providers.Match{}, err
		}
		url := gjson.GetBytes(content, "env.ANTHROPIC_BASE_URL").String()
		if url == "" {
			return providers.Match{Kind: providers.MatchNone}, nil
		}
		return s.provs.MatchBaseURL(url)
	}
	return providers.Match{Kind: providers.MatchNone}, nil
}

// keysFor 给 .claude.json 补上 keys 模式的受管键（spec §3.1）。
func keysFor(path string) []string {
	if strings.HasSuffix(path, ".claude.json") && !strings.Contains(path, "/") {
		return []string{"mcpServers"}
	}
	return nil
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("importer: 生成采集 token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// uniqueName 在重名时追加后缀。配置集名有唯一索引，重复导入不该直接失败。
func uniqueName(app core.App, base string) string {
	name := base
	for i := 2; i < 100; i++ {
		if r, _ := app.FindFirstRecordByData("config_sets", "name", name); r == nil {
			return name
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
	return base
}
