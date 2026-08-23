# 子计划 06 · agent render / restore 两个秘密键

**前置**：02（`protocol` 词法）。**不依赖** 03–05，可与它们并行。
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`render.Values` / `secrets.File` 去掉 `Creds`；秘密档从「全部凭据 +
一个 auth_token」变成**恰好两个键**（`claude.auth_token` / `openai.api_key`）；
`watcher` / `syncer` / `agent/drift.go` 三处接线跟进；`orciny` 版本号抬到 `0.3.0`。

**这个子计划里唯一会造成安全事故的点是 Task 1**：漏掉 `openai.api_key`，
那把 key 就落进尽力档——尽力档的语义是「替不掉也放行」，等于**有机会被明文
上传**（spec §3.4）。两个端点在这一点上没有区别，必须有测试。

`render.Render` 本身**一行不动**：它的 lookup 早就是通用的（M1.5 spec §4.1）。
本子计划改的全是「值从哪来」与「哪些值必须还原」。

---

### Task 1: 秘密档变成两个端点限定名

**Files:**
- Modify: `agent/internal/render/restore.go`
- Test: `agent/internal/render/restore_test.go`

**Interfaces:**
- Consumes: `protocol.ProviderKeys` / `RefProvider` / `RefVar`
- Produces: `render.Values{Vars, Provider}`（`Creds` 删除）；
  包内 `secretKeys = []string{"claude.auth_token", "openai.api_key"}`

- [ ] **Step 1: 写失败的测试**

改写 `agent/internal/render/restore_test.go`：删掉所有构造 `Creds:` 的用例
（它们测的是被废止的实体），把 `Provider: map[string]string{"auth_token": …}`
改成端点限定名。然后追加：

```go
// 两个端点的 key **都**是秘密档：必须还原，还原不了就 Safe=false。
// 一把 API key 落进尽力档就等于有机会被明文上传（M1.6 spec §3.4）。
func TestBothEndpointKeysAreSecrets(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.auth_token": "sk-claude-abcdef123456",
		"openai.api_key":    "sk-openai-zyxwvu654321",
	}}
	content := []byte("claude=sk-claude-abcdef123456 openai=sk-openai-zyxwvu654321")

	res := render.Restore(content, v)
	require.True(t, res.Safe)
	require.Equal(t,
		"claude={{provider.claude.auth_token}} openai={{provider.openai.api_key}}",
		string(res.Content))
}

// 秘密档的最后一道闸：任一已知秘密值仍能被搜到即判定还原失败。
func TestUnrestorableOpenAIKeyMakesUnsafe(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		// 值恰好落在 "{" 后面，替换后往返律破掉 → Safe=false
		"openai.api_key": "sk-openai-zyxwvu654321",
	}}
	res := render.Restore([]byte("{sk-openai-zyxwvu654321"), v)
	require.False(t, res.Safe, "还原不干净时调用方不得上报内容")
}

// 其余七个键进尽力档：受 MinVarLen 约束、替不掉也放行。
func TestSevenNonSecretProviderKeysAreBestEffort(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.base_url":     "https://open.bigmodel.cn/api/anthropic",
		"claude.model":        "glm-5.2",
		"claude.model_opus":   "glm-5.2",
		"claude.model_sonnet": "glm-5.2",
		"claude.model_haiku":  "glm-4.7",
		"openai.base_url":     "https://open.bigmodel.cn/api/paas/v4",
		"openai.model":        "glm-4.7",
	}}
	res := render.Restore([]byte("url=https://open.bigmodel.cn/api/anthropic"), v)
	require.True(t, res.Safe, "尽力档替不掉也不影响 Safe")
	require.Contains(t, string(res.Content), "{{provider.claude.base_url}}")
}

// 同值去重按 ProviderKeys 的下标定序，留下靠前的那个（M1.5 的 rankOf 语义不变，
// 只是名字变长了）。四槽同值时留下的是 claude.model。
func TestSameValueKeepsEarlierProviderKey(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.model":        "glm-5.2",
		"claude.model_opus":   "glm-5.2",
		"claude.model_sonnet": "glm-5.2",
		"claude.model_haiku":  "glm-5.2",
	}}
	res := render.Restore([]byte("model=glm-5.2"), v)
	require.Equal(t, "model={{provider.claude.model}}", string(res.Content))
}

// 两个端点用同一把 key 时，留下的是 ProviderKeys 里靠前的那个
// ——claude.auth_token 排在 openai.api_key 前面（spec §7）。
func TestSameKeyOnBothEndpointsKeepsClaude(t *testing.T) {
	v := render.Values{Provider: map[string]string{
		"claude.auth_token": "sk-shared-abcdef123456",
		"openai.api_key":    "sk-shared-abcdef123456",
	}}
	res := render.Restore([]byte("k=sk-shared-abcdef123456"), v)
	require.True(t, res.Safe)
	require.Equal(t, "k={{provider.claude.auth_token}}", string(res.Content))
}

// 往返律：restore 的产物必须能被 render 回原内容（M1 spec §6.1）。
func TestRoundTripWithBothEndpoints(t *testing.T) {
	v := render.Values{
		Vars: map[string]string{"workspace": "orciny-main"},
		Provider: map[string]string{
			"claude.auth_token": "sk-claude-abcdef123456",
			"openai.api_key":    "sk-openai-zyxwvu654321",
			"claude.base_url":   "https://open.bigmodel.cn/api/anthropic",
		},
	}
	original := []byte("ws=orciny-main claude=sk-claude-abcdef123456 " +
		"openai=sk-openai-zyxwvu654321 url=https://open.bigmodel.cn/api/anthropic")

	res := render.Restore(original, v)
	require.True(t, res.Safe)

	back, err := render.Render(res.Content, func(r protocol.Ref) (string, bool) {
		switch r.Kind {
		case protocol.RefVar:
			s, ok := v.Vars[r.Name]
			return s, ok
		case protocol.RefProvider:
			s, ok := v.Provider[r.Name]
			return s, ok
		}
		return "", false
	})
	require.NoError(t, err)
	require.Equal(t, original, back)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./agent/internal/render/ -v
```

Expected: FAIL —— `render.Values` 还有 `Creds` 字段、`authTokenKey` 还是
`"auth_token"`、`protocol.RefCred` 未定义（编译错）。

- [ ] **Step 3: 实现**

Modify `agent/internal/render/restore.go`：

```go
// secretKeys 是 provider 里走秘密档的两个键。
//
// **两个都是**（M1.6 spec §3.4）。秘密档的语义是「必须还原，还原不了就拒绝
// 上传」，尽力档是「尽力替回、替不掉也放行」（M1 spec §7）。一把 API key
// 落进尽力档就等于有机会被明文上传，两个端点在这一点上没有区别。
var secretKeys = []string{"claude.auth_token", "openai.api_key"}

func isSecretKey(name string) bool {
	for _, k := range secretKeys {
		if k == name {
			return true
		}
	}
	return false
}
```

```go
// Values 是还原时可用的全部值。
//
// provider 的九个值被分派进两档（M1.6 spec §3.4）：
// claude.auth_token 与 openai.api_key 是秘密，进秘密档（必须还原，
// 失败即 Safe=false）；base_url、四个模型槽与 openai.model 不是秘密，
// 进变量档（尽力而为，受 MinVarLen 约束）。
//
// Creds 字段随 {{cred.*}} 一起删除。
type Values struct {
	Vars     map[string]string
	Provider map[string]string
}
```

三个 bucket 函数：

```go
// secretBucket 是必须还原的值：两个端点的 key。
//
// 它们顶着 provider. 前缀，但都是秘密，不能因为前缀就当成普通值。
func (v Values) secretBucket() []replacement {
	out := make([]replacement, 0, len(secretKeys))
	for _, k := range secretKeys {
		val := v.Provider[k]
		if val == "" {
			continue
		}
		out = append(out, replacement{
			value: val,
			token: tokenOf(protocol.RefProvider, k),
			rank:  rankOf(protocol.RefProvider, k),
		})
	}
	return dedupeByValue(sortByValueLenDesc(out))
}

// varBucket 是尽力而为的值：全部机器变量，加上 provider 里除两把 key 之外的七个。
func (v Values) varBucket() []replacement {
	rest := make(map[string]string, len(v.Provider))
	for k, val := range v.Provider {
		if !isSecretKey(k) {
			rest[k] = val
		}
	}
	out := append(replacements(v.Vars, protocol.RefVar),
		replacements(rest, protocol.RefProvider)...)
	return dedupeByValue(sortByValueLenDesc(out))
}

// secretValues 是「还原后不允许再被搜到」的值集合（M1 spec §6.4 的最后一道闸）。
func (v Values) secretValues() []string {
	out := make([]string, 0, len(secretKeys))
	for _, k := range secretKeys {
		if val := v.Provider[k]; val != "" {
			out = append(out, val)
		}
	}
	return out
}
```

`restoreLookup` 去掉 `case protocol.RefCred` 那一支。

`rankOf` **一行不改**——它已经是按 `protocol.ProviderKeys` 的下标定序，
换成九个端点限定名之后自动给出 `claude.auth_token` 早于 `openai.api_key`、
`claude.model` 早于三个副槽。把注释里的例子更新一下：

```go
// rankOf 给同值替换项定序：变量恒为 0，provider 按 ProviderKeys 的下标顺延。
// 名字不在白名单里（不该发生）时排到最后。
//
// 两处需要它：四个模型槽常常填同一个值（留下 claude.model，那是主槽）；
// 两个端点共用同一把 key 时留下 claude.auth_token（它在 ProviderKeys 里靠前）。
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./agent/internal/render/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add agent/internal/render/
git commit -m "feat(agent): 两个端点的 key 都进秘密档"
```

---

### Task 2: `secrets.json` 去掉 `creds`

**Files:**
- Modify: `agent/internal/secrets/secrets.go`
- Test: `agent/internal/secrets/secrets_test.go`

**Interfaces:**
- Consumes: `protocol.RefVar` / `RefMachine` / `RefProvider`
- Produces: `secrets.File{Vars, Machine, Provider}`

**老 agent 写过的文件里多一个 `creds` 键，反序列化忽略**（spec §3.5）。
不写清理代码——那个文件下一次 `Save` 就自然没有它了。

- [ ] **Step 1: 写失败的测试**

追加到 `agent/internal/secrets/secrets_test.go`（并删掉引用 `Creds` 的既有用例）：

```go
func TestFileHasNoCredsField(t *testing.T) {
	typ := reflect.TypeOf(secrets.File{})
	_, ok := typ.FieldByName("Creds")
	require.False(t, ok, "Creds 字段随 {{cred.*}} 一起删除")
}

// 老 agent 写下的 secrets.json 里多一个 creds 键，读回来忽略即可，不能报错。
func TestLoadIgnoresLegacyCredsKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(secrets.Path(dir), []byte(`{
		"creds":{"zhipu":"sk-old"},
		"vars":{"workspace":"main"},
		"provider":{"claude.base_url":"https://x.example"}
	}`), 0o600))

	f, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, "main", f.Vars["workspace"])
	require.Equal(t, "https://x.example", f.Provider["claude.base_url"])
}

func TestLookupResolvesEndpointQualifiedProviderKeys(t *testing.T) {
	f := &secrets.File{Provider: map[string]string{
		"claude.auth_token": "sk-claude-abcdef123456",
		"openai.api_key":    "sk-openai-zyxwvu654321",
	}}
	v, ok := f.Lookup(protocol.Ref{Kind: protocol.RefProvider, Name: "openai.api_key"})
	require.True(t, ok)
	require.Equal(t, "sk-openai-zyxwvu654321", v)
}

// 保存出来的 JSON 里不许再有 creds 键。
func TestSaveWritesNoCredsKey(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, secrets.Save(dir, &secrets.File{
		Vars: map[string]string{"workspace": "main"},
	}))
	b, err := os.ReadFile(secrets.Path(dir))
	require.NoError(t, err)
	require.NotContains(t, string(b), `"creds"`)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./agent/internal/secrets/ -v
```

Expected: FAIL。

- [ ] **Step 3: 实现**

Modify `agent/internal/secrets/secrets.go`：

```go
type File struct {
	Vars    map[string]string `json:"vars"`
	Machine map[string]string `json:"machine"`
	// Provider 是服务绑定注入的端点限定名（M1.6 spec §3.1）。
	// claude.auth_token 与 openai.api_key 都是秘密，因此本文件仍然一律 0600。
	//
	// 原 Creds 字段随 {{cred.*}} 一起删除。老 agent 写下的文件里多一个
	// creds 键，反序列化自然忽略，不清洗（spec §3.5）。
	Provider map[string]string `json:"provider"`
}
```

`Load` 的空值兜底去掉 `f.Creds`；`Lookup` 去掉 `case protocol.RefCred`；
`Equal` 去掉 `sameMap(f.Creds, o.Creds)`。

包注释里那句「上报前要把磁盘里的真实值替回 `{{cred.x}}`」改成
「替回 `{{provider.claude.auth_token}}` 一类的占位符」。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./agent/internal/secrets/ -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add agent/internal/secrets/
git commit -m "feat(agent): secrets.json 去掉 creds"
```

---

### Task 3: `watcher` / `syncer` / `agent/drift.go` 三处接线

**Files:**
- Modify: `agent/internal/watcher/scan.go:340-375`
- Modify: `agent/internal/syncer/syncer.go:404-430`
- Modify: `agent/drift.go:139-143`
- Test: `agent/internal/watcher/*_test.go`、`agent/internal/syncer/*_test.go`（改既有断言）

**Interfaces:**
- Consumes: Task 1 / Task 2 的类型
- Produces: 无新接口

- [ ] **Step 1: 写失败的测试**

这三处都是纯接线，没有新行为可测。把既有测试里构造 `Creds:` 的地方删掉，
并在 `syncer` 的测试里追加一条，锁住「快照的 Provider 原样落进 secrets.json」：

```go
func TestSaveSecretsCarriesBothEndpoints(t *testing.T) {
	dir := t.TempDir()
	s := newSyncerAt(t, dir)

	require.NoError(t, s.SaveSecretsForTest(protocol.ConfigSnapshot{
		Variables: map[string]string{"workspace": "main"},
		Provider: map[string]string{
			"claude.auth_token": "sk-claude-abcdef123456",
			"openai.api_key":    "sk-openai-zyxwvu654321",
		},
	}))

	f, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-abcdef123456", f.Provider["claude.auth_token"])
	require.Equal(t, "sk-openai-zyxwvu654321", f.Provider["openai.api_key"])
	require.Equal(t, "main", f.Vars["workspace"])
}
```

> `SaveSecretsForTest` 是 `saveSecrets` 的导出封装，加到 `agent/export_test.go`
> 里（那个文件已经在干这类事）。`newSyncerAt` 按 `syncer` 测试里现有的构造
> 方式包一层。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./agent/... -v 2>&1 | head -40
```

Expected: FAIL（编译错：`Creds` 字段不存在、`snap.Credentials` 不存在）。

- [ ] **Step 3: 实现**

`agent/internal/watcher/scan.go`：

```go
// valuesOf 把秘密缓存整理成还原用的值集合。
// provider 的分档由 render.Values 内部完成（M1.6 spec §3.4），这里不做判断。
func valuesOf(sec *secrets.File) render.Values {
	if sec == nil {
		return render.Values{}
	}
	return render.Values{Vars: sec.Vars, Provider: sec.Provider}
}
```

`cloneSecrets` 去掉 `Creds: copyMap(sec.Creds),`。

`agent/internal/syncer/syncer.go` 的 `saveSecrets`：

```go
	f := &secrets.File{
		Vars:     snap.Variables,
		Provider: snap.Provider,
		Machine: map[string]string{
			"name":     name,
			"hostname": hostname,
			"os":       runtime.GOOS,
			"arch":     runtime.GOARCH,
		},
	}
	if f.Vars == nil {
		f.Vars = map[string]string{}
	}
	if f.Provider == nil {
		f.Provider = map[string]string{}
	}
```

（`f.Creds` 的赋值与兜底一起删掉。函数注释里的「凭据」改成「秘密」。）

`agent/drift.go`：

```go
				res := render.Restore(raw, render.Values{
					Vars: sec.Vars, Provider: sec.Provider,
				})
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./agent/... -v
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add agent/
git commit -m "refactor(agent): 还原取值接线去掉凭据"
```

---

### Task 4: 版本号抬一档

**Files:**
- Modify: `orciny.go`
- Test: `orciny_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `Version = "0.3.0"`；`MinProviderAgentVersion = 0.3.0`

**为什么必须抬**（spec §9 最后一行）：老 agent 认不得端点限定名，
拿到 `{{provider.claude.base_url}}` 会在 `Parse` 阶段报「不是内置名」。
定向门槛（`configsync.checkAgentVersion`）拦在下发之前，
「不下发」好过「发下去让它渲染失败再回滚」。

**`MinAgentVersion` 不动**（保持 `0.1.0`）：它在握手层拦截，一抬就把所有
低版本 agent 挡在门外——包括那些指派的配置集根本没用绑定的机器。

- [ ] **Step 1: 写失败的测试**

追加到 `orciny_test.go`：

```go
func TestVersionsForM16(t *testing.T) {
	require.Equal(t, "0.3.0", orciny.Version)
	require.Equal(t, "0.3.0", orciny.MinProviderAgentVersion.String(),
		"老 agent 认不得端点限定名，门槛要跟着抬一档")
	require.Equal(t, "0.1.0", orciny.MinAgentVersion.String(),
		"握手层门槛不动：一抬就把无关机器也挡在门外")
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./ -run VersionsForM16 -v
```

Expected: FAIL（`0.2.0` != `0.3.0`）。

- [ ] **Step 3: 实现**

Modify `orciny.go`：

```go
var Version = "0.3.0"
```

```go
// MinProviderAgentVersion 是能渲染 {{provider.<endpoint>.*}} 的最低 agent 版本。
//
// M1.6 抬到 0.3.0：占位符改成端点限定名，0.2.x 的 agent 在 Parse 阶段就会
// 报「不是内置名」（M1.6 spec §9）。
//
// 定向门槛：只在快照真的带绑定、且确实引用了 {{provider.*}} 时才检查
// （M1.5 spec §10）。惩罚面因此不会扩大到无关机器。
var MinProviderAgentVersion = semver.MustParse("0.3.0")
```

`MinAgentVersion` 的注释补一句：

```go
// **M1.5 与 M1.6 都不动它**（保持 0.1.0）。
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./... 
```

Expected: 与版本相关的用例全绿。若 `testsupport` 的端到端因 03–05 还没做完
而红，那属于对应子计划，不要在这里改。

- [ ] **Step 5: 提交**

```bash
git add orciny.go orciny_test.go
git commit -m "chore: 版本抬到 0.3.0，provider 门槛跟进"
```
