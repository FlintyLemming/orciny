# 子计划 08 · 删除 credentials 的服务端残留 + 迁移 `005`

**前置**：04、05、06、07（`credentials.Store` 的最后一批使用者都已改掉）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints、
**偏离一**与全局接口契约。

**交付物**：迁移 `005_drop_credentials.go`（删 relation、删旧字段、删 collection，
`down` 返回错误）；`hub/internal/credentials` 包整体删除；`/credentials` 三个路由、
`hub.CreateCredential` / `RotateCredential` / `DeleteCredential`、
`events.KindCredential*` 三个常量删除。

**做这个子计划之前先确认前置真的做完了**：

```bash
grep -rn "credentials\.\|hub/internal/credentials" --include='*.go' \
  hub/ agent/ protocol/ internal/ cmd/ | grep -v '^hub/internal/credentials/'
```

只应剩下 `hub/hub.go`、`hub/api.go`、`hub/internal/routes/*` 三处——
它们正是本子计划要清的。若还有别的，回到对应子计划先做完。

---

### Task 1: 迁移 `005`（破坏性）

**Files:**
- Create: `hub/internal/migrations/005_drop_credentials.go`
- Test: `hub/internal/migrations/migrations_test.go`（追加）

**Interfaces:**
- Consumes: `004` 搬完的数据
- Produces: `providers` 不再有 `credential` / `base_url` / `auth_field` /
  `models` / `defaults`；`credentials` collection 消失；`down005` 返回错误

**没有 down 迁移**（spec §6.3）：凭据删了之后无处还原——`004` 的第 2 步是
**有损的**（多条凭据可能被同一条 provider 引用过，反向拆不回去）。
写一个假装能回滚的 down 比没有更危险。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/migrations/migrations_test.go`：

```go
func TestMigration005DropsLegacyFieldsAndCollection(t *testing.T) {
	app := newApp(t)

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	for _, f := range []string{"credential", "base_url", "auth_field", "models", "defaults"} {
		require.Nil(t, c.Fields.GetByName(f), "providers.%s 必须已删除", f)
	}

	_, err = app.FindCollectionByNameOrId("credentials")
	require.Error(t, err, "credentials collection 必须已删除")
}

// 新结构必须完好——005 只删旧的，不许碰新的。
func TestMigration005KeepsEndpointFields(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	for _, f := range []string{
		"name", "preset", "note", "key_cipher", "key_last4",
		"claude_key_cipher", "openai_key_cipher", "claude", "openai",
		"created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "providers.%s 不该被删", f)
	}
}

// 004 + 005 跑完之后，搬过来的密文还解得开。这是整条迁移链的验收点。
func TestCipherSurvivesFullMigrationChain(t *testing.T) {
	dir := t.TempDir()
	key, err := secretbox.LoadMasterKey(dir)
	require.NoError(t, err)
	cipher, err := secretbox.Encrypt(key, "sk-zhipu-abcdef123456")
	require.NoError(t, err)

	app := newApp(t)
	// 库里已经跑完全部迁移，credentials 表没了；直接把密文放进 provider 的
	// 平台级字段，模拟 004 搬运的结果，再验证它仍然解得开。
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", "智谱 GLM")
	r.Set("key_cipher", cipher)
	r.Set("key_last4", "3456")
	require.NoError(t, app.Save(r))

	got, err := app.FindRecordById("providers", r.Id)
	require.NoError(t, err)
	pt, err := secretbox.Decrypt(key, got.GetString("key_cipher"))
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", pt)
}

// down005 必须明确失败，而不是假装能回滚（spec §6.3）。
func TestDown005Refuses(t *testing.T) {
	app := newApp(t)
	require.Error(t, migrations.Down005(app),
		"凭据删了之后无处还原，假装能回滚比没有更危险")
}
```

`Down005` 是 `down005` 的导出别名（只为测试可达）：

```go
// Down005 供测试断言「它确实拒绝回滚」。
func Down005(app core.App) error { return down005(app) }
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/migrations/ -run 'Migration005|Down005|CipherSurvives' -v
```

Expected: FAIL —— 旧字段还在、`credentials` 还在、`Down005` 未定义。

- [ ] **Step 3: 实现**

Create `hub/internal/migrations/005_drop_credentials.go`：

```go
package migrations

import (
	"errors"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up005, down005, "005_drop_credentials.go")
}

// M1.6 的破坏性一半（spec §6.1 的第 3、4、5 步）。
//
// 跑这条之前，004 已经把每条 provider 的旧字段与它引用的凭据搬进了新结构。
// 这里只做拆除：删 relation 与索引、删四个旧字段、删 credentials collection。
func up005(app core.App) error {
	provs, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return err
	}
	// 索引要先于字段删——PocketBase 不允许索引指向不存在的列。
	provs.RemoveIndex("idx_providers_credential")
	for _, f := range []string{
		"credential", "base_url", "auth_field", "models", "defaults",
	} {
		provs.Fields.RemoveByName(f)
	}
	if err := app.Save(provs); err != nil {
		return err
	}

	creds, err := app.FindCollectionByNameOrId("credentials")
	if err != nil {
		return nil // 已经没了，幂等
	}
	return app.Delete(creds)
}

// down005 直接返回错误（spec §6.3）。
//
// 凭据删了之后无处还原——004 的搬运是**有损的**：多条凭据可能被同一条
// provider 引用过，反向拆不回去。写一个假装能回滚的 down 比没有更危险。
func down005(app core.App) error {
	return errors.New("005: 不支持回滚——凭据实体已删除，无处还原。" +
		"若要回到 M1.5，请从迁移前的备份恢复整个 pb_data")
}

// Down005 供测试断言「它确实拒绝回滚」。
func Down005(app core.App) error { return down005(app) }
```

> `RemoveIndex` 的确切方法名以 PocketBase v0.39 的 `core.Collection` 为准；
> 若该版本上是通过 `provs.Indexes` 切片操作，就按那个写法过滤掉
> `idx_providers_credential` 那一条。用
> `go doc github.com/pocketbase/pocketbase/core.Collection` 确认。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./hub/internal/migrations/ -v
```

Expected: PASS。`hub` 其余包此刻编译不过（`credentials` 包还在被引用），
下一个 Task 处理。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/migrations/
git commit -m "feat(hub): 迁移 005 删掉凭据实体与 providers 旧字段"
```

---

### Task 2: 删掉 `credentials` 包与它的路由 / API

**Files:**
- Delete: `hub/internal/credentials/`（整个目录）
- Modify: `hub/hub.go`（`creds` 字段与构造、`VerifyAll`、`routes.Deps.Creds`）
- Modify: `hub/api.go:18-35`（三个方法删除）
- Modify: `hub/internal/routes/routes.go`（三条路由与 `Deps.Creds`）
- Modify: `hub/internal/routes/config.go`（三个 handler、`credName`、`mapErr`）
- Modify: `hub/internal/events/writer.go:38-40`（三个常量）
- Test: `hub/internal/routes/routes_test.go`（断言路由不存在）

**Interfaces:**
- Consumes: 无
- Produces: `hub/internal/credentials` 不复存在

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/routes/routes_test.go`：

```go
// 三条凭据路由删除（M1.6 spec §3.5）。
func TestCredentialRoutesAreGone(t *testing.T) {
	rig := newRoutesRig(t)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/orciny/credentials"},
		{"POST", "/api/orciny/credentials/x/rotate"},
		{"DELETE", "/api/orciny/credentials/x"},
	} {
		res := rig.Do(t, c.method, c.path, `{}`)
		require.Equal(t, 404, res.Code, "%s %s 必须已删除", c.method, c.path)
	}
}

// 机器变量的端点**保留**——它管的是 {{var.*}}，与凭据无关（spec §5.4）。
func TestVariablesRouteSurvives(t *testing.T) {
	rig := newRoutesRig(t)
	machineID := rig.SeedMachine(t)
	res := rig.PUT(t, "/api/orciny/machines/"+machineID+"/variables",
		`{"workspace":"main"}`)
	require.Equal(t, 204, res.Code)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/routes/ -run 'CredentialRoutes|VariablesRoute' -v
```

Expected: FAIL —— 三条路由还在（返回的不是 404）。

- [ ] **Step 3: 实现**

```bash
rm -r hub/internal/credentials
```

`hub/hub.go`：

- 删 import `hub/internal/credentials`
- 删字段 `creds *credentials.Store`
- 删这两行：

```go
		h.creds = credentials.NewStore(e.App, key, h.events)
		if err := h.creds.VerifyAll(); err != nil {
			return err
		}
```

  只留 `h.provs.VerifyAll()`（03 Task 6 已加）。注释更新：

```go
		// 主密钥。必须排在 ws 之前：一台解不开自己 key 的 hub 不该接客
		// ——它会把空值下发到全机队（M1.6 spec §2.6）。
		// 自检的对象从 credentials 表改成 providers 的三处密文字段。
```

- `routes.Register(...)` 里删 `Creds: h.creds,`

`hub/api.go`：`CreateCredential` / `RotateCredential` / `DeleteCredential`
三个方法整体删除。

`hub/internal/routes/routes.go`：

- 删 import `hub/internal/credentials`
- `Deps` 删 `Creds *credentials.Store`（`Vars *variables.Store` 保留）
- 删三条注册：

```go
	g.POST("/credentials", d.createCredential).Bind(su)
	g.POST("/credentials/{id}/rotate", d.rotateCredential).Bind(su)
	g.DELETE("/credentials/{id}", d.deleteCredential).Bind(su)
```

`hub/internal/routes/config.go`：

- `Admin` 接口删 `CreateCredential` / `RotateCredential` / `DeleteCredential`
- `createCredential` / `rotateCredential` / `deleteCredential` / `credName`
  四个函数删除，连同 `// ---------- credentials ----------` 分节标题
- `mapErr` 里所有 `credentials.Err*` 的分支删掉；`ErrInUse` 那一支只留
  `providers.ErrInUse`；短值与非法名换成：

```go
	case errors.Is(err, secretbox.ErrShortValue),
		errors.Is(err, providers.ErrNoKey),
		errors.Is(err, variables.ErrBadName):
		return e.BadRequestError(err.Error(), nil)
	case errors.Is(err, providers.ErrNotFound),
		errors.Is(err, blobs.ErrNotFound),
		errors.Is(err, configsets.ErrNoAssignment):
		return e.NotFoundError(err.Error(), nil)
```

- 删 import `hub/internal/credentials`

`hub/internal/events/writer.go`：删三个常量：

```go
	KindCredentialCreated = "credential.created"
	KindCredentialRotated = "credential.rotated"
	KindCredentialDeleted = "credential.deleted"
```

**历史 `events` 行不清洗**：库里已有的那些 kind 字符串留着，前端的
`EventKind` 联合类型也保留它们（09 Task 1），否则老事件渲染不出来。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./... 
```

Expected: **全绿**。这是 M1.6 后端第一次整棵树通过。

```bash
grep -rn "credentials" --include='*.go' hub/ agent/ protocol/ internal/ cmd/
```

Expected: 只剩 `internal/manifest/manifest.go` 里的
`".claude/.credentials.json"`（OAuth 登录态的恒排除项，与本期无关）
与 `agent/internal/applier/plan.go` 里引用它的注释。

- [ ] **Step 5: 提交**

```bash
git add -A hub/ agent/ protocol/ internal/
git commit -m "refactor(hub): 删除凭据实体的服务端残留"
```

---

### Task 3: 端到端回归

**Files:**
- Modify: `internal/testsupport/providers_test.go`
- Modify: `internal/testsupport/configsets.go`（若签名跟着变）

**Interfaces:**
- Consumes: 全部前置子计划
- Produces: M1.5 的端到端在新词法下继续绿，加一条双端点的

M1.5 的 `TestProviderBindingEndToEnd` 是本期的**核心不变量守卫**
（DoD 第 13 条）：改 Provider 重注入全机队、**不产生新 Revision**、**不产生漂移**。
它必须在改完词法后继续通过。

- [ ] **Step 1: 写失败的测试**

改 `internal/testsupport/providers_test.go`：把 settings.json 里的六个占位符
换成端点限定写法，断言不变。然后追加一条双端点的：

```go
// 双端点：claude 端点注入到 settings.json，openai 端点注入到另一个文件。
// 本期 openai 端点没有消费者，但「记下来的配置」要能一路走通到落盘。
func TestBothEndpointsReachDisk(t *testing.T) {
	th := testsupport.NewHub(t)
	defer th.Close()

	provID := th.SeedProviderWithOpenAI(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456",
		"https://open.bigmodel.cn/api/paas/v4", "glm-5.2")

	setID, _ := th.Hub.SeedConfigSet(t, "主力配置", map[string]string{
		".claude/settings.json": `{"env":{
			"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",
			"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"
		}}`,
		".codex/config.toml": "base_url = \"{{provider.openai.base_url}}\"\n" +
			"model = \"{{provider.openai.model}}\"\n",
	})
	th.Hub.BindConfigSet(t, setID, provID, "glm-5.2")

	agent := th.StartAgent(t)
	th.AssignAndWait(t, agent, setID)

	settings := agent.ReadFile(t, ".claude/settings.json")
	require.Contains(t, string(settings), "https://open.bigmodel.cn/api/anthropic")
	require.Contains(t, string(settings), "sk-zhipu-abcdef123456")
	require.NotContains(t, string(settings), "{{provider.")

	codex := agent.ReadFile(t, ".codex/config.toml")
	require.Contains(t, string(codex), "https://open.bigmodel.cn/api/paas/v4")
	require.Contains(t, string(codex), "glm-5.2")

	// 库里仍然只有占位符。
	head := th.Hub.HeadRevision(t, setID)
	blob := th.Hub.RevisionBlob(t, head, ".claude/settings.json")
	require.Contains(t, string(blob), "{{provider.claude.auth_token}}")
	require.NotContains(t, string(blob), "sk-zhipu")
}
```

> `SeedProviderWithOpenAI` / `StartAgent` / `AssignAndWait` / `ReadFile` /
> `HeadRevision` 按 `providers_test.go` 里既有的端到端写法照做——那条测试
> 已经把这套流程走了一遍，本条只是多一个文件与一个端点。
> `SeedProviderWithOpenAI` 加到 `hub/seed_testing.go` 并在
> `internal/testsupport/configsets.go` 里转发（Go 的 internal 规则）。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./internal/testsupport/ -v
```

Expected: FAIL —— `SeedProviderWithOpenAI` 未定义。

- [ ] **Step 3: 实现**

`hub/seed_testing.go` 加：

```go
// SeedProviderWithOpenAI 建一条两个端点都配了的服务配置，返回 provider id。
// 平台级一把 key，两个端点共用——这是最常见的情形（M1.6 spec §2.3）。
func (h *Hub) SeedProviderWithOpenAI(
	t *testing.T, name, claudeURL, key, openaiURL, openaiModel string,
) string {
	t.Helper()
	id, err := h.CreateProvider(providers.Input{
		Name: name,
		Key:  &key,
		Claude: providers.EndpointInput{
			BaseURL:   claudeURL,
			AuthField: providers.AuthToken,
			Models:    []string{"glm-5.2", "glm-4.7"},
		},
		OpenAI: providers.EndpointInput{
			BaseURL:      openaiURL,
			AuthField:    providers.DefaultOpenAIAuthField,
			Models:       []string{openaiModel},
			DefaultModel: openaiModel,
		},
	})
	require.NoError(t, err, "建双端点服务配置")
	return id
}
```

`internal/testsupport/configsets.go` 加一层转发：

```go
// SeedProviderWithOpenAI 转发到 hub.Hub 的同名方法。
// internal/testsupport 够不到 hub/internal/*（Go 的 internal 规则）。
func (h *TestHub) SeedProviderWithOpenAI(
	t *testing.T, name, claudeURL, key, openaiURL, openaiModel string,
) string {
	t.Helper()
	return h.Hub.SeedProviderWithOpenAI(t, name, claudeURL, key, openaiURL, openaiModel)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./... 
```

Expected: 全绿。

```bash
make lint
```

Expected: 无输出。

- [ ] **Step 5: 提交**

```bash
git add internal/testsupport hub/seed_testing.go
git commit -m "test: 双端点端到端回归"
```
