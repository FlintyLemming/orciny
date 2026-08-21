package credentials

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
)

// MinValueLen 是凭据值的长度下限（spec §6.4）。
//
// 理由不是安全强度，是**还原的可靠性**：一个 6 字符的「密钥」在文件里
// 到处误匹配的风险，远大于它作为密钥的价值。
const MinValueLen = 8

var (
	ErrNotFound   = errors.New("credentials: 凭据不存在")
	ErrInUse      = errors.New("credentials: 凭据仍被引用")
	ErrShortValue = errors.New("credentials: 凭据值过短")
	ErrBadName    = errors.New("credentials: 凭据名非法")
)

type Store struct {
	app core.App
	key []byte
	ev  *events.Writer
}

func NewStore(app core.App, key []byte, ev *events.Writer) *Store {
	return &Store{app: app, key: key, ev: ev}
}

// refsField 是 revisions.refs 与 config_sets.draft_refs 的形状。
type refsField struct {
	Creds        []string `json:"creds"`
	Vars         []string `json:"vars"`
	ProviderKeys []string `json:"provider_keys"`
}

// VerifyAll 在启动时逐条解密自检。
//
// 库里有凭据但主密钥解不开时必须拒绝启动（spec §6.6）：「备份恢复到新机器
// 时忘了带密钥文件」是最可能的翻车场景，宁可开不了机，也不能让用户以为
// 一切正常然后把空值下发到全机队。
func (s *Store) VerifyAll() error {
	recs, err := s.app.FindAllRecords("credentials")
	if err != nil {
		return fmt.Errorf("credentials: 读取凭据库: %w", err)
	}
	for _, r := range recs {
		if _, err := Decrypt(s.key, r.GetString("cipher_value")); err != nil {
			return fmt.Errorf("credentials: 凭据 %q 解密失败——主密钥不匹配。"+
				"若是从备份恢复，请把原机器的 %s 或 %s 一并带过来: %w",
				r.GetString("name"), KeyFileName, EnvKeyName, err)
		}
	}
	return nil
}

func (s *Store) Create(name, value, note string) (*core.Record, error) {
	if !validName(name) {
		return nil, fmt.Errorf("%w: %q（只允许 [A-Za-z0-9_-]+）", ErrBadName, name)
	}
	if len(value) < MinValueLen {
		return nil, fmt.Errorf("%w: 至少 %d 个字符", ErrShortValue, MinValueLen)
	}
	enc, err := Encrypt(s.key, value)
	if err != nil {
		return nil, err
	}
	c, err := s.app.FindCollectionByNameOrId("credentials")
	if err != nil {
		return nil, fmt.Errorf("credentials: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("cipher_value", enc)
	r.Set("last4", last4(value))
	r.Set("note", note)
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("credentials: 创建 %s: %w", name, err)
	}
	// 事件里只有名字与末四位，不含值。
	if err := s.ev.Write(events.KindCredentialCreated, "", map[string]any{
		"name": name, "last4": r.GetString("last4"),
	}); err != nil {
		s.app.Logger().Warn("写 credential.created 事件失败", "error", err)
	}
	return r, nil
}

// Rotate 换值。轮换**不产生新 Revision**（产品 §4.5 的硬要求），
// 下发由调用方发一次 ConfigNotify 完成（spec §5.1）。
func (s *Store) Rotate(name, value string) error {
	if len(value) < MinValueLen {
		return fmt.Errorf("%w: 至少 %d 个字符", ErrShortValue, MinValueLen)
	}
	r, err := s.find(name)
	if err != nil {
		return err
	}
	enc, err := Encrypt(s.key, value)
	if err != nil {
		return err
	}
	r.Set("cipher_value", enc)
	r.Set("last4", last4(value))
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("credentials: 轮换 %s: %w", name, err)
	}
	if err := s.ev.Write(events.KindCredentialRotated, "", map[string]any{
		"name": name, "last4": r.GetString("last4"),
	}); err != nil {
		s.app.Logger().Warn("写 credential.rotated 事件失败", "error", err)
	}
	return nil
}

// Delete 删凭据。被任何 head revision 或 draft 引用时拒绝。
func (s *Store) Delete(name string) error {
	r, err := s.find(name)
	if err != nil {
		return err
	}
	sets, _, providerIDs, err := s.ReferencedBy(name)
	if err != nil {
		return err
	}
	if len(sets) > 0 {
		return fmt.Errorf("%w: 被 %d 个配置集的当前版本或草稿引用", ErrInUse, len(sets))
	}
	if len(providerIDs) > 0 {
		return fmt.Errorf("%w: 被 %d 个 AI 服务配置引用", ErrInUse, len(providerIDs))
	}
	if err := s.app.Delete(r); err != nil {
		return fmt.Errorf("credentials: 删除 %s: %w", name, err)
	}
	if err := s.ev.Write(events.KindCredentialDeleted, "", map[string]any{"name": name}); err != nil {
		s.app.Logger().Warn("写 credential.deleted 事件失败", "error", err)
	}
	return nil
}

func (s *Store) Value(name string) (string, error) {
	r, err := s.find(name)
	if err != nil {
		return "", err
	}
	return Decrypt(s.key, r.GetString("cipher_value"))
}

// Values 批量取值。未知的名字直接跳过——调用方（configsync）拿到的是
// 「这个 Revision 引用到的凭据里，当前还存在的那些」，缺的那些由 agent
// 在渲染时报未定义引用（spec §6.1）。
func (s *Store) Values(names []string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, n := range names {
		v, err := s.Value(n)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[n] = v
	}
	return out, nil
}

// ReferencedBy 返回引用该凭据的配置集 id（head revision 或 draft）、
// 历史 revision id，以及**引用它的 AI 服务配置 id**（M1.5 spec §5.3）。
//
// 前两者查的是 refs 字段而不是全库扫 blob 内容（spec §6.5）；
// Provider 引用凭据的方式是 relation 字段，不在 refs 里，因此单独扫一遍——
// 不扫的话一条正在被 Provider 使用的凭据可以被删掉，
// 然后全机队在下次重注入时拿到空值。
func (s *Store) ReferencedBy(name string) ([]string, []string, []string, error) {
	var setIDs, revIDs []string

	sets, err := s.app.FindAllRecords("config_sets")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("credentials: 扫描配置集: %w", err)
	}
	headIDs := map[string]string{} // revision id → config set id
	for _, set := range sets {
		var draft refsField
		_ = set.UnmarshalJSONField("draft_refs", &draft)
		if contains(draft.Creds, name) {
			setIDs = append(setIDs, set.Id)
		}
		if h := set.GetString("head"); h != "" {
			headIDs[h] = set.Id
		}
	}

	revs, err := s.app.FindAllRecords("revisions")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("credentials: 扫描版本: %w", err)
	}
	for _, rev := range revs {
		var refs refsField
		_ = rev.UnmarshalJSONField("refs", &refs)
		if !contains(refs.Creds, name) {
			continue
		}
		if setID, isHead := headIDs[rev.Id]; isHead {
			if !contains(setIDs, setID) {
				setIDs = append(setIDs, setID)
			}
			continue
		}
		revIDs = append(revIDs, rev.Id)
	}

	cred, err := s.find(name)
	if err != nil {
		return nil, nil, nil, err
	}
	provs, err := s.app.FindRecordsByFilter("providers",
		"credential = {:c}", "name", 0, 0, map[string]any{"c": cred.Id})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("credentials: 扫描服务配置: %w", err)
	}
	providerIDs := make([]string, 0, len(provs))
	for _, p := range provs {
		providerIDs = append(providerIDs, p.Id)
	}
	return setIDs, revIDs, providerIDs, nil
}

// ValueByID 按记录 id 取值。providers.credential 是 relation 字段，
// 存的是 id 而不是名字，因此下发时走这条而不是 Value(name)。
func (s *Store) ValueByID(id string) (string, error) {
	r, err := s.app.FindRecordById("credentials", id)
	if err != nil || r == nil {
		return "", fmt.Errorf("%w: id %s", ErrNotFound, id)
	}
	return Decrypt(s.key, r.GetString("cipher_value"))
}

func (s *Store) find(name string) (*core.Record, error) {
	r, err := s.app.FindFirstRecordByData("credentials", "name", name)
	if err != nil || r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return r, nil
}

func last4(v string) string {
	if len(v) <= 4 {
		return v
	}
	return v[len(v)-4:]
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
