# 子计划 03 · 凭据库、机器变量与主密钥

**前置**：01（`credentials` / `variables` collection）、02（`protocol.Refs`）
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`hub/internal/credentials`——主密钥加载、AES-GCM 加解密、凭据 CRUD 与引用保护、机器变量读写、启动自检；以及把自检接进 `hub.Attach` 的 `OnServe`。

**为什么这些必须一次做对（spec §1.4 / §6.6）**：主密钥丢失时若静默把凭据当成损坏数据，用户会以为一切正常，然后把空值下发到全机队。凭据引用口径错了会让「删掉一个还在用的凭据」变成一次全机队故障。

---

### Task 1: 主密钥与 AES-GCM

**Files:**
- Create: `hub/internal/credentials/crypto.go`
- Test: `hub/internal/credentials/crypto_test.go`

**Interfaces:**
- Produces:
  ```go
  func LoadMasterKey(dataDir string) ([]byte, error)
  func Encrypt(key []byte, plaintext string) (string, error)
  func Decrypt(key []byte, cipherB64 string) (string, error)
  const KeyFileName = "secret.key"
  ```

来源优先级（spec §6.6）：1. 环境变量 `ORCINY_SECRET_KEY`（32 字节的 base64）；2. `pb_data/secret.key`（0600，首次启动自动生成）。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/credentials/crypto_test.go`：

```go
package credentials_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/credentials"
)

func TestLoadMasterKeyGeneratesFileOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	k1, err := credentials.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Len(t, k1, 32)

	info, err := os.Stat(filepath.Join(dir, credentials.KeyFileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "主密钥文件必须 0600")

	k2, err := credentials.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, k1, k2, "第二次加载必须拿到同一把钥匙")
}

func TestLoadMasterKeyPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	t.Setenv("ORCINY_SECRET_KEY", base64.StdEncoding.EncodeToString(want))

	got, err := credentials.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoFileExists(t, filepath.Join(dir, credentials.KeyFileName),
		"环境变量给了钥匙就不该再生成文件")
}

func TestLoadMasterKeyRejectsBadEnv(t *testing.T) {
	t.Setenv("ORCINY_SECRET_KEY", "not-base64!!")
	_, err := credentials.LoadMasterKey(t.TempDir())
	require.Error(t, err)

	t.Setenv("ORCINY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("太短")))
	_, err = credentials.LoadMasterKey(t.TempDir())
	require.ErrorContains(t, err, "32")
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	for _, v := range []string{"", "sk-ant-abc123", "含中文的值", string(make([]byte, 4096))} {
		enc, err := credentials.Encrypt(key, v)
		require.NoError(t, err)
		require.NotContains(t, enc, v, "密文里不该出现明文")

		dec, err := credentials.Decrypt(key, enc)
		require.NoError(t, err)
		require.Equal(t, v, dec)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	a, err := credentials.Encrypt(key, "same")
	require.NoError(t, err)
	b, err := credentials.Encrypt(key, "same")
	require.NoError(t, err)
	require.NotEqual(t, a, b, "同一明文两次加密必须不同（nonce 每次新生成）")
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	k1, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	k2, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	enc, err := credentials.Encrypt(k1, "sk-secret")
	require.NoError(t, err)
	_, err = credentials.Decrypt(k2, enc)
	require.Error(t, err, "换了主密钥必须解不开，而不是解出垃圾")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/credentials/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `hub/internal/credentials/crypto.go`：

```go
// Package credentials 管凭据库与机器变量。
//
// 凭据以 AES-GCM 密文落库，明文只在两处出现：hub 内存里（组装 ConfigSnapshot 时）
// 与 agent 的 ~/.orciny/secrets.json（spec §6.2）。Revision 与 blob 里
// **永远只有占位符**。
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// KeyFileName 是主密钥在数据目录下的文件名。
const KeyFileName = "secret.key"

// EnvKeyName 是主密钥的环境变量名，值为 32 字节的 base64。
const EnvKeyName = "ORCINY_SECRET_KEY"

const keySize = 32

// LoadMasterKey 按 spec §6.6 的优先级取主密钥：
// 环境变量 → 数据目录里的 secret.key（不存在则生成，0600）。
func LoadMasterKey(dataDir string) ([]byte, error) {
	if v := os.Getenv(EnvKeyName); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("credentials: %s 不是合法的 base64: %w", EnvKeyName, err)
		}
		if len(k) != keySize {
			return nil, fmt.Errorf("credentials: %s 解出 %d 字节，应为 %d", EnvKeyName, len(k), keySize)
		}
		return k, nil
	}

	path := filepath.Join(dataDir, KeyFileName)
	b, err := os.ReadFile(path)
	if err == nil {
		k, derr := base64.StdEncoding.DecodeString(string(b))
		if derr != nil || len(k) != keySize {
			return nil, fmt.Errorf("credentials: %s 内容损坏（应为 %d 字节的 base64）", path, keySize)
		}
		return k, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("credentials: 读取主密钥: %w", err)
	}

	k := make([]byte, keySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("credentials: 生成主密钥: %w", err)
	}
	if err := atomicfile.Write(path, []byte(base64.StdEncoding.EncodeToString(k)), 0o600); err != nil {
		return nil, fmt.Errorf("credentials: 写入主密钥: %w", err)
	}
	return k, nil
}

// Encrypt 返回 base64(nonce ‖ ciphertext ‖ tag)。
func Encrypt(key []byte, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("credentials: 生成 nonce: %w", err)
	}
	out := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func Decrypt(key []byte, cipherB64 string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return "", fmt.Errorf("credentials: 密文不是合法的 base64: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("credentials: 密文过短")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		// 不带上任何密文片段：错误信息会进日志。
		return "", fmt.Errorf("credentials: 解密失败（主密钥不匹配或数据损坏）")
	}
	return string(pt), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("credentials: 主密钥应为 %d 字节，实际 %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credentials: 构造 AES: %w", err)
	}
	return cipher.NewGCM(block)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/credentials/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/credentials/
git commit -m "feat: 主密钥加载与 AES-GCM 加解密"
```

---

### Task 2: 凭据 Store 与引用保护

**Files:**
- Create: `hub/internal/credentials/store.go`
- Test: `hub/internal/credentials/store_test.go`

**Interfaces:**
- Consumes: `blobs`（不需要）、`events.Writer`、Task 1 的加解密
- Produces: `NewStore` / `VerifyAll` / `Create` / `Rotate` / `Delete` / `Value` / `Values` / `ReferencedBy` / `MinValueLen` / 四个哨兵错误（逐字符见 00-overview）

**规则**

- 名字字符集 `[A-Za-z0-9_-]+`，与占位符语法一致（spec §4.1）。
- 值长度 < `MinValueLen`（8）拒绝创建：6 字符的「密钥」在文件里到处误匹配的风险远大于它作为密钥的价值（spec §6.4）。
- 被任何 **head revision 或 draft** 引用的凭据不允许删除；历史版本的引用只出现在 `ReferencedBy` 的返回值里，不阻止删除（spec §6.5）。
- `last4` 是 UI 唯一能看到的东西。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/credentials/store_test.go`：

```go
package credentials_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newStore(t *testing.T) (*tests.TestApp, *credentials.Store) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	return app, credentials.NewStore(app, key, events.NewWriter(app))
}

func TestCreateStoresCipherAndLast4(t *testing.T) {
	_, s := newStore(t)
	r, err := s.Create("anthropic_key", "sk-ant-abcdefgh1234", "主力")
	require.NoError(t, err)
	require.Equal(t, "1234", r.GetString("last4"))
	require.NotContains(t, r.GetString("cipher_value"), "sk-ant", "库里不能出现明文")

	v, err := s.Value("anthropic_key")
	require.NoError(t, err)
	require.Equal(t, "sk-ant-abcdefgh1234", v)
}

func TestCreateRejectsShortValueAndBadName(t *testing.T) {
	_, s := newStore(t)
	_, err := s.Create("k", "short7x", "")
	require.ErrorIs(t, err, credentials.ErrShortValue)

	_, err = s.Create("bad name", "longenough123", "")
	require.ErrorIs(t, err, credentials.ErrBadName)
}

func TestRotateChangesValueOnly(t *testing.T) {
	_, s := newStore(t)
	r, err := s.Create("k", "sk-oldvalue-1111", "note")
	require.NoError(t, err)

	require.NoError(t, s.Rotate("k", "sk-newvalue-2222"))

	v, err := s.Value("k")
	require.NoError(t, err)
	require.Equal(t, "sk-newvalue-2222", v)

	got, err := s.Values([]string{"k"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"k": "sk-newvalue-2222"}, got)
	require.Equal(t, "k", r.GetString("name"))
}

func TestValuesSkipsUnknownNames(t *testing.T) {
	_, s := newStore(t)
	_, err := s.Create("known", "sk-known-9999", "")
	require.NoError(t, err)

	got, err := s.Values([]string{"known", "gone"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"known": "sk-known-9999"}, got)
}

// 被 head revision 或 draft 引用的凭据不许删（spec §6.5）。
func TestDeleteIsBlockedWhileReferenced(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("in_use", "sk-inuse-7777", "")
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "s")
	set.Set("draft_refs", map[string]any{"creds": []string{"in_use"}, "vars": []string{}})
	require.NoError(t, app.Save(set))

	require.ErrorIs(t, s.Delete("in_use"), credentials.ErrInUse)

	setIDs, _, err := s.ReferencedBy("in_use")
	require.NoError(t, err)
	require.Equal(t, []string{set.Id}, setIDs)

	// 草稿不再引用之后就可以删
	set.Set("draft_refs", map[string]any{"creds": []string{}, "vars": []string{}})
	require.NoError(t, app.Save(set))
	require.NoError(t, s.Delete("in_use"))
}

// 历史版本的引用只警告不阻止：历史不可变，但也不会再被下发。
func TestDeleteAllowedWhenOnlyOldRevisionReferences(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("old_only", "sk-oldonly-5555", "")
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "s")
	require.NoError(t, app.Save(set))

	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	old := core.NewRecord(revs)
	old.Set("config_set", set.Id)
	old.Set("seq", 1)
	old.Set("refs", map[string]any{"creds": []string{"old_only"}, "vars": []string{}})
	old.Set("source", "publish")
	require.NoError(t, app.Save(old))

	head := core.NewRecord(revs)
	head.Set("config_set", set.Id)
	head.Set("seq", 2)
	head.Set("refs", map[string]any{"creds": []string{}, "vars": []string{}})
	head.Set("source", "publish")
	require.NoError(t, app.Save(head))

	set.Set("head", head.Id)
	require.NoError(t, app.Save(set))

	_, revIDs, err := s.ReferencedBy("old_only")
	require.NoError(t, err)
	require.Equal(t, []string{old.Id}, revIDs, "历史引用要报出来")
	require.NoError(t, s.Delete("old_only"), "但不阻止删除")
}

func TestVerifyAllFailsOnWrongMasterKey(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("k", "sk-value-3333", "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyAll())

	other, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	bad := credentials.NewStore(app, other, events.NewWriter(app))
	require.Error(t, bad.VerifyAll(), "换了主密钥必须报错，不能静默把凭据当损坏数据")
}

func TestEventsAreWritten(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("k", "sk-value-4444", "")
	require.NoError(t, err)
	require.NoError(t, s.Rotate("k", "sk-value-5555"))
	require.NoError(t, s.Delete("k"))

	recs, err := app.FindAllRecords("events")
	require.NoError(t, err)
	var kinds []string
	for _, r := range recs {
		kinds = append(kinds, r.GetString("kind"))
	}
	require.Subset(t, kinds, []string{
		events.KindCredentialCreated, events.KindCredentialRotated, events.KindCredentialDeleted,
	})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/credentials/ -run Store -v`
Expected: FAIL，`undefined: credentials.NewStore`

- [ ] **Step 3: 实现**

Create `hub/internal/credentials/store.go`：

```go
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
	Creds []string `json:"creds"`
	Vars  []string `json:"vars"`
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
	sets, _, err := s.ReferencedBy(name)
	if err != nil {
		return err
	}
	if len(sets) > 0 {
		return fmt.Errorf("%w: 被 %d 个配置集的当前版本或草稿引用", ErrInUse, len(sets))
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

// ReferencedBy 返回引用该凭据的配置集 id（head revision 或 draft）
// 与历史 revision id。查的是 refs 字段而不是全库扫 blob 内容（spec §6.5）。
func (s *Store) ReferencedBy(name string) ([]string, []string, error) {
	var setIDs, revIDs []string

	sets, err := s.app.FindAllRecords("config_sets")
	if err != nil {
		return nil, nil, fmt.Errorf("credentials: 扫描配置集: %w", err)
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
		return nil, nil, fmt.Errorf("credentials: 扫描版本: %w", err)
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
	return setIDs, revIDs, nil
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/credentials/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/credentials/
git commit -m "feat: 凭据 CRUD、引用保护与启动自检"
```

---

### Task 3: 机器变量

**Files:**
- Create: `hub/internal/credentials/variables.go`
- Test: `hub/internal/credentials/variables_test.go`

**Interfaces:**
- Produces: `MachineVariables` / `SetVariable` / `DeleteVariable`

变量与凭据同属「值的来源」，因此放同一个包；但它**不加密**——非秘密，秘密请用凭据（spec §4.1）。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/credentials/variables_test.go`：

```go
package credentials_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

func TestVariablesRoundTrip(t *testing.T) {
	app, s := newStore(t)

	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(mc)
	m.Set("fingerprint", "fp")
	m.Set("pub_key", "pk")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	require.NoError(t, s.SetVariable(m.Id, "workspace", "main"))
	require.NoError(t, s.SetVariable(m.Id, "tier", "1"))

	got, err := s.MachineVariables(m.Id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"workspace": "main", "tier": "1"}, got)

	// 同 key 再设一次是更新，不是新增
	require.NoError(t, s.SetVariable(m.Id, "workspace", "side"))
	got, err = s.MachineVariables(m.Id)
	require.NoError(t, err)
	require.Equal(t, "side", got["workspace"])

	require.NoError(t, s.DeleteVariable(m.Id, "tier"))
	got, err = s.MachineVariables(m.Id)
	require.NoError(t, err)
	require.NotContains(t, got, "tier")
}

func TestMachineVariablesOfUnknownMachineIsEmpty(t *testing.T) {
	_, s := newStore(t)
	got, err := s.MachineVariables("nope")
	require.NoError(t, err)
	require.Empty(t, got)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/credentials/ -run Variables -v`
Expected: FAIL，`s.SetVariable undefined`

- [ ] **Step 3: 实现**

Create `hub/internal/credentials/variables.go`：

```go
package credentials

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// 机器变量：非秘密、不加密。秘密请用凭据（spec §4.1）。
//
// 与凭据同包，是因为两者共同构成「占位符的取值来源」，
// configsync 组装 ConfigSnapshot 时一起取。

func (s *Store) MachineVariables(machineID string) (map[string]string, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m}", "key", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("credentials: 读取机器变量: %w", err)
	}
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = r.GetString("value")
	}
	return out, nil
}

func (s *Store) SetVariable(machineID, key, value string) error {
	if !validName(key) {
		return fmt.Errorf("%w: 变量名 %q（只允许 [A-Za-z0-9_-]+）", ErrBadName, key)
	}
	r, err := s.findVariable(machineID, key)
	if err != nil {
		return err
	}
	if r == nil {
		c, cerr := s.app.FindCollectionByNameOrId("variables")
		if cerr != nil {
			return fmt.Errorf("credentials: 找不到 collection: %w", cerr)
		}
		r = core.NewRecord(c)
		r.Set("machine", machineID)
		r.Set("key", key)
	}
	r.Set("value", value)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("credentials: 保存变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) DeleteVariable(machineID, key string) error {
	r, err := s.findVariable(machineID, key)
	if err != nil || r == nil {
		return err
	}
	if err := s.app.Delete(r); err != nil {
		return fmt.Errorf("credentials: 删除变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) findVariable(machineID, key string) (*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m} && key = {:k}", "", 1, 0,
		map[string]any{"m": machineID, "k": key})
	if err != nil {
		return nil, fmt.Errorf("credentials: 查询变量: %w", err)
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return recs[0], nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/credentials/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/credentials/
git commit -m "feat: 机器变量读写"
```

---

### Task 4: 接进 hub 装配，主密钥解不开就拒绝启动

**Files:**
- Modify: `hub/hub.go`
- Test: `hub/hub_test.go`

**Interfaces:**
- Consumes: `credentials.LoadMasterKey` / `NewStore` / `VerifyAll`
- Produces: `Hub` 新增未导出字段 `creds *credentials.Store`；`OnServe` 里在 `identity.Load` 之后、`ws.NewHandler` 之前完成加载与自检

- [ ] **Step 1: 写失败的测试**

追加到 `hub/hub_test.go`：

```go
// 库里有凭据但主密钥解不开时必须拒绝启动（spec §6.6）。
func TestServeFailsWhenMasterKeyDoesNotMatch(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	// 先用当前主密钥存一条凭据
	key, err := credentials.LoadMasterKey(app.DataDir())
	require.NoError(t, err)
	store := credentials.NewStore(app, key, events.NewWriter(app))
	_, err = store.Create("k", "sk-value-1234", "")
	require.NoError(t, err)

	// 再把主密钥换掉，模拟「恢复备份时忘了带密钥文件」
	t.Setenv(credentials.EnvKeyName,
		base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)
	t.Cleanup(h.Shutdown)

	se := new(core.ServeEvent)
	se.App = app
	router, err := apis.NewRouter(app)
	require.NoError(t, err)
	se.Router = router
	se.Server = &http.Server{}

	err = app.OnServe().Trigger(se, func(*core.ServeEvent) error { return nil })
	require.Error(t, err, "主密钥不匹配必须让 serve 失败")
	require.ErrorContains(t, err, "主密钥")
}
```

按需补 import：`bytes`、`encoding/base64`、`net/http`、`github.com/pocketbase/pocketbase/apis`、`github.com/pocketbase/pocketbase/core`、`github.com/pocketbase/pocketbase/tests`、`hub/internal/credentials`、`hub/internal/events`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/ -run MasterKey -v`
Expected: FAIL，`OnServe().Trigger` 返回 nil（此刻还没有自检）

- [ ] **Step 3: 实现**

在 `hub/hub.go` 的 `Hub` 结构体加字段：

```go
	creds *credentials.Store
```

在 `OnServe` 的 `identity.Load` 成功之后、`h.machines.ResetGhosts()` 之前插入：

```go
		// 凭据主密钥。必须排在 ws 之前：一台解不开凭据的 hub 不该接客
		// ——它会把空值下发到全机队（spec §6.6）。
		key, err := credentials.LoadMasterKey(e.App.DataDir())
		if err != nil {
			return fmt.Errorf("加载凭据主密钥: %w", err)
		}
		h.creds = credentials.NewStore(e.App, key, h.events)
		if err := h.creds.VerifyAll(); err != nil {
			return err
		}
```

import 追加 `"github.com/FlintyLemming/orciny/hub/internal/credentials"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/...`
Expected: PASS

- [ ] **Step 5: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add hub/
git commit -m "feat: hub 启动时加载主密钥并自检凭据"
```
