# 子计划 01 · `secretbox` / `variables` 拆包

**前置**：无（与 02 可并行）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：新包 `hub/internal/secretbox`（AES-GCM、主密钥、`MinValueLen`、`Last4`）与
`hub/internal/variables`（机器变量 Store）；`hub/internal/credentials` 只剩 `store.go`
（本子计划**不删它**，它在 08 才消失）。

**这是纯搬迁，不改行为。** spec §8 把它排在第一位的理由是：后面所有改动都要
往这两个新包上落脚。`KeyFileName` / `EnvKeyName` 两个常量的**值一个字符都不能变**
——存量密文继续解得开，是 §6 迁移能「直接搬密文、不用解密」的前提。

为什么拆成两个包而不是把机器变量也塞进 `secretbox`（spec §2.5）：机器变量与
秘密加密**没有任何共享代码**，留在一起只会让 `secretbox` 这个安全敏感的包
混进不相干的东西。凭据没了之后，「两者共同构成占位符的取值来源」这个当初的
理由也不成立了。

---

### Task 1: `secretbox` 包

**Files:**
- Create: `hub/internal/secretbox/secretbox.go`
- Create: `hub/internal/secretbox/secretbox_test.go`
- Delete: `hub/internal/credentials/crypto.go`
- Delete: `hub/internal/credentials/crypto_test.go`
- Modify: `hub/internal/credentials/store.go`（改用 `secretbox.*`，本期临时状态）
- Modify: `hub/hub.go:110`（`credentials.LoadMasterKey` → `secretbox.LoadMasterKey`）
- Modify: `hub/internal/importer/service.go:188,191`（`credentials.MinValueLen` → `secretbox.MinValueLen`）

**Interfaces:**
- Consumes: `internal/atomicfile.Write`
- Produces: `secretbox.KeyFileName` / `EnvKeyName` / `MinValueLen` / `ErrShortValue` /
  `LoadMasterKey` / `Encrypt` / `Decrypt` / `Last4`

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/secretbox/secretbox_test.go`（内容照搬
`hub/internal/credentials/crypto_test.go` 并改包名，另加 `Last4` 与常量值锁定）：

```go
package secretbox_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
)

// 常量的值是**存量数据的兼容契约**（spec §2.5）：改了它们，
// 从备份恢复的库就再也解不开了。锁死。
func TestConstantsAreUnchanged(t *testing.T) {
	require.Equal(t, "secret.key", secretbox.KeyFileName)
	require.Equal(t, "ORCINY_SECRET_KEY", secretbox.EnvKeyName)
	require.Equal(t, 8, secretbox.MinValueLen)
}

func TestLoadMasterKeyGeneratesFileOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	k1, err := secretbox.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Len(t, k1, 32)

	info, err := os.Stat(filepath.Join(dir, secretbox.KeyFileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "主密钥文件必须 0600")

	k2, err := secretbox.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, k1, k2, "第二次加载必须拿到同一把钥匙")
}

func TestLoadMasterKeyPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	t.Setenv(secretbox.EnvKeyName, base64.StdEncoding.EncodeToString(want))

	got, err := secretbox.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoFileExists(t, filepath.Join(dir, secretbox.KeyFileName),
		"环境变量给了钥匙就不该再生成文件")
}

func TestLoadMasterKeyRejectsBadEnv(t *testing.T) {
	t.Setenv(secretbox.EnvKeyName, "not-base64!!")
	_, err := secretbox.LoadMasterKey(t.TempDir())
	require.Error(t, err)

	t.Setenv(secretbox.EnvKeyName, base64.StdEncoding.EncodeToString([]byte("short")))
	_, err = secretbox.LoadMasterKey(t.TempDir())
	require.Error(t, err, "长度不对的钥匙必须拒绝")
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	ct, err := secretbox.Encrypt(key, "sk-zhipu-abcdef123456")
	require.NoError(t, err)
	require.NotContains(t, ct, "sk-zhipu", "密文里不许出现明文片段")

	pt, err := secretbox.Decrypt(key, ct)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", pt)
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	a, err := secretbox.Encrypt(key, "same-value")
	require.NoError(t, err)
	b, err := secretbox.Encrypt(key, "same-value")
	require.NoError(t, err)
	require.NotEqual(t, a, b, "每次加密都要用新 nonce")
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	k1, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	k2, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	ct, err := secretbox.Encrypt(k1, "sk-zhipu-abcdef123456")
	require.NoError(t, err)
	_, err = secretbox.Decrypt(k2, ct)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sk-zhipu",
		"错误信息会进日志，不许带上任何密文或明文片段")
}

func TestLast4(t *testing.T) {
	require.Equal(t, "3456", secretbox.Last4("sk-zhipu-abcdef123456"))
	require.Equal(t, "abc", secretbox.Last4("abc"), "短于四位时原样返回")
	require.Equal(t, "", secretbox.Last4(""))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/secretbox/ -v
```

Expected: FAIL，`hub/internal/secretbox` 包不存在（`no Go files` / build error）。

- [ ] **Step 3: 实现**

Create `hub/internal/secretbox/secretbox.go`。内容是
`hub/internal/credentials/crypto.go` 逐行搬过来，改包名与错误前缀，
再补 `MinValueLen` / `ErrShortValue` / `Last4`：

```go
// Package secretbox 管秘密的对称加密与主密钥。
//
// 秘密以 AES-GCM 密文落库，明文只在两处出现：hub 内存里（组装 ConfigSnapshot
// 时）与 agent 的 ~/.orciny/secrets.json（M1 spec §6.2）。Revision 与 blob 里
// **永远只有占位符**。
//
// 这个包是从 credentials 拆出来的（M1.6 spec §2.5）：凭据实体没了，
// 但「加密落库、末四位回显、启动自检」这套复用降回代码层，由本包承担。
// KeyFileName 与 EnvKeyName 的值**不变**，存量密文继续解得开。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// KeyFileName 是主密钥在数据目录下的文件名。
const KeyFileName = "secret.key"

// EnvKeyName 是主密钥的环境变量名，值为 32 字节的 base64。
const EnvKeyName = "ORCINY_SECRET_KEY"

// MinValueLen 是秘密值的长度下限（M1 spec §6.4）。
//
// 理由不是安全强度，是**还原的可靠性**：一个 6 字符的「密钥」在文件里
// 到处误匹配的风险，远大于它作为密钥的价值。provider 的 key 一样要被
// agent 还原，这条约束原样适用（M1.6 spec §2.5）。
const MinValueLen = 8

// ErrShortValue 供路由层映射成 400。
var ErrShortValue = errors.New("secretbox: 值过短")

const keySize = 32

// LoadMasterKey 按 M1 spec §6.6 的优先级取主密钥：
// 环境变量 → 数据目录里的 secret.key（不存在则生成，0600）。
func LoadMasterKey(dataDir string) ([]byte, error) {
	if v := os.Getenv(EnvKeyName); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("secretbox: %s 不是合法的 base64: %w", EnvKeyName, err)
		}
		if len(k) != keySize {
			return nil, fmt.Errorf("secretbox: %s 解出 %d 字节，应为 %d", EnvKeyName, len(k), keySize)
		}
		return k, nil
	}

	path := filepath.Join(dataDir, KeyFileName)
	b, err := os.ReadFile(path)
	if err == nil {
		k, derr := base64.StdEncoding.DecodeString(string(b))
		if derr != nil || len(k) != keySize {
			return nil, fmt.Errorf("secretbox: %s 内容损坏（应为 %d 字节的 base64）", path, keySize)
		}
		return k, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("secretbox: 读取主密钥: %w", err)
	}

	k := make([]byte, keySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("secretbox: 生成主密钥: %w", err)
	}
	if err := atomicfile.Write(path, []byte(base64.StdEncoding.EncodeToString(k)), 0o600); err != nil {
		return nil, fmt.Errorf("secretbox: 写入主密钥: %w", err)
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
		return "", fmt.Errorf("secretbox: 生成 nonce: %w", err)
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
		return "", fmt.Errorf("secretbox: 密文不是合法的 base64: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("secretbox: 密文过短")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		// 不带上任何密文片段：错误信息会进日志。
		return "", fmt.Errorf("secretbox: 解密失败（主密钥不匹配或数据损坏）")
	}
	return string(pt), nil
}

// Last4 返回末四位，UI 回显用。短于四位时原样返回。
func Last4(v string) string {
	if len(v) <= 4 {
		return v
	}
	return v[len(v)-4:]
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secretbox: 主密钥应为 %d 字节，实际 %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: 构造 AES: %w", err)
	}
	return cipher.NewGCM(block)
}
```

- [ ] **Step 4: 删掉旧文件并把三处调用改指过来**

```bash
rm hub/internal/credentials/crypto.go hub/internal/credentials/crypto_test.go
```

`hub/internal/credentials/store.go`：加 import
`"github.com/FlintyLemming/orciny/hub/internal/secretbox"`，然后

- `Decrypt(s.key, ...)` → `secretbox.Decrypt(s.key, ...)`（三处：`VerifyAll`、`Value`、`ValueByID`）
- `Encrypt(s.key, value)` → `secretbox.Encrypt(s.key, value)`（两处：`Create`、`Rotate`）
- `MinValueLen` → `secretbox.MinValueLen`（两处），并把包内的 `const MinValueLen = 8` 删掉
- `KeyFileName` / `EnvKeyName` → `secretbox.KeyFileName` / `secretbox.EnvKeyName`（`VerifyAll` 的错误信息里）
- `last4(value)` → `secretbox.Last4(value)`，并把包内的 `func last4` 删掉

`hub/hub.go`：加 import，把

```go
		key, err := credentials.LoadMasterKey(e.App.DataDir())
```

改成

```go
		key, err := secretbox.LoadMasterKey(e.App.DataDir())
```

`hub/internal/importer/service.go`：把 `credentials.MinValueLen`（两处）改成
`secretbox.MinValueLen`，并加 import。

`hub/internal/credentials/store_test.go` 里若引用了 `credentials.MinValueLen`，
一并改指 `secretbox.MinValueLen`。

- [ ] **Step 5: 运行测试确认全绿**

```bash
go test -tags=testing ./... 
```

Expected: PASS。`secretbox` 新包 8 个用例通过；`credentials` 剩下的用例
（`store_test.go` / `variables_test.go`）行为不变，全部仍然通过。

- [ ] **Step 6: 提交**

```bash
git add hub/internal/secretbox hub/internal/credentials hub/hub.go hub/internal/importer/service.go
git commit -m "refactor(hub): AES-GCM 与主密钥拆出 secretbox 包"
```

---

### Task 2: `variables` 包

**Files:**
- Create: `hub/internal/variables/variables.go`
- Create: `hub/internal/variables/variables_test.go`
- Delete: `hub/internal/credentials/variables.go`
- Delete: `hub/internal/credentials/variables_test.go`
- Modify: `hub/hub.go`（新增 `vars *variables.Store` 字段与构造）
- Modify: `hub/internal/configsync/service.go:44,119`（`Deps.Creds.MachineVariables` → `Deps.Vars.MachineVariables`）
- Modify: `hub/internal/routes/routes.go:41`（`Deps.Creds` 旁加 `Deps.Vars`）
- Modify: `hub/internal/routes/config.go:359-370`（`setVariables` 改用 `d.Vars`）

**Interfaces:**
- Consumes: `core.App`
- Produces: `variables.Store` / `NewStore` / `MachineVariables` / `SetVariable` /
  `DeleteVariable` / `ErrBadName`

> `configsync.Deps` 本任务只**加** `Vars` 字段，`Creds` 字段留到 05 再删——
> 那时 `refs.Creds` 的最后一个使用者才消失。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/variables/variables_test.go`：

```go
package variables_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/variables"
)

func newStore(t *testing.T) (*tests.TestApp, *variables.Store, string) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(mc)
	m.Set("fingerprint", "fp")
	m.Set("pub_key", "pk")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	return app, variables.NewStore(app), m.Id
}

func TestVariablesRoundTrip(t *testing.T) {
	_, s, machineID := newStore(t)

	require.NoError(t, s.SetVariable(machineID, "workspace", "main"))
	require.NoError(t, s.SetVariable(machineID, "tier", "1"))

	got, err := s.MachineVariables(machineID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"workspace": "main", "tier": "1"}, got)

	// 同 key 再设一次是更新，不是新增
	require.NoError(t, s.SetVariable(machineID, "workspace", "side"))
	got, err = s.MachineVariables(machineID)
	require.NoError(t, err)
	require.Equal(t, "side", got["workspace"])
	require.Len(t, got, 2)
}

func TestDeleteVariable(t *testing.T) {
	_, s, machineID := newStore(t)
	require.NoError(t, s.SetVariable(machineID, "workspace", "main"))
	require.NoError(t, s.DeleteVariable(machineID, "workspace"))

	got, err := s.MachineVariables(machineID)
	require.NoError(t, err)
	require.Empty(t, got)

	// 删不存在的变量是幂等的，不是错误
	require.NoError(t, s.DeleteVariable(machineID, "workspace"))
}

func TestSetVariableRejectsBadName(t *testing.T) {
	_, s, machineID := newStore(t)
	err := s.SetVariable(machineID, "not a name", "v")
	require.ErrorIs(t, err, variables.ErrBadName)
}

// 机器变量与秘密加密没有任何共享代码——这就是它没有留在 secretbox 里的理由
// （M1.6 spec §2.5）。这条测试锁住「值是明文落库」这个既定语义。
func TestVariablesAreStoredInPlaintext(t *testing.T) {
	app, s, machineID := newStore(t)
	require.NoError(t, s.SetVariable(machineID, "workspace", "main"))

	recs, err := app.FindAllRecords("variables")
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, "main", recs[0].GetString("value"))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/variables/ -v
```

Expected: FAIL，`hub/internal/variables` 包不存在。

- [ ] **Step 3: 实现**

Create `hub/internal/variables/variables.go`：

```go
// Package variables 管机器变量（{{var.*}}）。
//
// 非秘密、不加密、按机器（M1 spec §4.1）。它当初与凭据同包，理由是
// 「两者共同构成占位符的取值来源」；凭据实体废止之后这个理由不成立，
// 而机器变量与秘密加密没有任何共享代码——留在一起只会让 secretbox
// 那个安全敏感的包混进不相干的东西（M1.6 spec §2.5）。
package variables

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

var ErrBadName = errors.New("variables: 变量名非法")

type Store struct {
	app core.App
}

func NewStore(app core.App) *Store { return &Store{app: app} }

func (s *Store) MachineVariables(machineID string) (map[string]string, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m}", "key", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("variables: 读取机器变量: %w", err)
	}
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = r.GetString("value")
	}
	return out, nil
}

func (s *Store) SetVariable(machineID, key, value string) error {
	if !validName(key) {
		return fmt.Errorf("%w: %q（只允许 [A-Za-z0-9_-]+）", ErrBadName, key)
	}
	r, err := s.find(machineID, key)
	if err != nil {
		return err
	}
	if r == nil {
		c, cerr := s.app.FindCollectionByNameOrId("variables")
		if cerr != nil {
			return fmt.Errorf("variables: 找不到 collection: %w", cerr)
		}
		r = core.NewRecord(c)
		r.Set("machine", machineID)
		r.Set("key", key)
	}
	r.Set("value", value)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("variables: 保存变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) DeleteVariable(machineID, key string) error {
	r, err := s.find(machineID, key)
	if err != nil || r == nil {
		return err
	}
	if err := s.app.Delete(r); err != nil {
		return fmt.Errorf("variables: 删除变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) find(machineID, key string) (*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m} && key = {:k}", "", 1, 0,
		map[string]any{"m": machineID, "k": key})
	if err != nil {
		return nil, fmt.Errorf("variables: 查询变量: %w", err)
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return recs[0], nil
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

- [ ] **Step 4: 删旧文件并接线**

```bash
rm hub/internal/credentials/variables.go hub/internal/credentials/variables_test.go
```

`hub/hub.go`：

- 结构体加字段 `vars *variables.Store`（挨着 `creds` 放）
- 在 `h.creds = credentials.NewStore(...)` 之后加一行 `h.vars = variables.NewStore(e.App)`
- `configsync.NewService(configsync.Deps{...})` 里加 `Vars: h.vars,`
- `routes.Register(e, routes.Deps{...})` 里加 `Vars: h.vars,`

`hub/internal/configsync/service.go`：`Deps` 加 `Vars *variables.Store`；
`allVars, err := s.d.Creds.MachineVariables(machineID)` 改成 `s.d.Vars.MachineVariables(machineID)`。

`hub/internal/routes/routes.go`：`Deps` 加 `Vars *variables.Store`。

`hub/internal/routes/config.go` 的 `setVariables`：

```go
func (d Deps) setVariables(e *core.RequestEvent) error {
	if d.Vars == nil {
		return e.InternalServerError("变量服务未就绪", nil)
	}
	machineID := e.Request.PathValue("id")
	var req map[string]string
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	for k, v := range req {
		if err := d.Vars.SetVariable(machineID, k, v); err != nil {
			return mapErr(e, err)
		}
	}
	return e.NoContent(http.StatusNoContent)
}
```

`mapErr` 里 `errors.Is(err, credentials.ErrBadName)` 那一支加上
`errors.Is(err, variables.ErrBadName)`（两者暂时并存，`credentials.ErrBadName`
在 08 才消失）。

配置里凡是构造 `configsync.Service` 的测试（`hub/internal/configsync/*_test.go`）
也要补上 `Vars:` 字段，否则 `MachineVariables` 会在 nil 指针上炸。

- [ ] **Step 5: 运行测试确认全绿**

```bash
go test -tags=testing ./...
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add hub/internal/variables hub/internal/credentials hub/hub.go hub/internal/configsync hub/internal/routes
git commit -m "refactor(hub): 机器变量拆出 variables 包"
```
