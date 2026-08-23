# M1.6 验收记录

对照 [00-overview.md](00-overview.md) 的十九条 DoD（源自
[M1.6 工程设计](../../specs/2026-08-22-provider-endpoints-design.md) §1.2 / §7 / §9）。

**执行日期：** 2026-08-22
**执行环境：** macOS（Darwin arm64）· Go 1.26.5 · Node（Vitest 4.1）
**分支：** `genesis` @ `1b26efb`
**说明：** 本轮**没有**多台异构真机，也**没有做浏览器目视走查**。
可在单机用自动化如实覆盖的条目已测；需要真机或人眼的条目一律标为
**待真机目视**并写清对应的自动化覆盖，不拿自动化冒充目视。

---

## 总表

| # | 标准 | 结论 | 依据 |
|---|---|---|---|
| 1 | 一条 provider 同时承载 claude 与 openai 两个端点；`name` 唯一索引仍在 | **通过（自动化）** | `providers.TestCreateStoresBothEndpoints`（两个端点各自解出 base_url / 末四位 / default_model）；`migrations.TestProviderNameIsUnique`（跨 004/005 之后同名仍被唯一索引拒绝） |
| 2 | 端点级 key 覆盖平台级；两级都空 + `base_url` 非空 → `providers.validate` 拒绝 | **通过（自动化）** | `providers.TestEndpointKeyOverridesPlatformKey`（claude 拿端点级、openai 回落平台级）、`TestCreateRejectsConfiguredEndpointWithoutAnyKey`（`ErrorIs ErrNoKey` 且错误串含 `claude`）、`TestEndpointOnlyKeyIsEnough` |
| 3 | 只配 claude 端点的 provider：`OpenAIOf(r).Configured() == false`，UI 显示置灰的「未配置」行 | **通过（自动化）/ 待真机目视** | `providers.TestCreateWithOnlyClaudeEndpoint`、`TestConfiguredIsBaseURLPresence`；前端 `Providers.test.tsx`「未配置的端点显示成置灰的「未配置」行」（断言 `endpoint-openai` 含「OpenAI」与「未配置」） |
| 4 | 九个端点限定名全部 parse 通过；`{{provider.base_url}}` 报「不是内置名」；`{{cred.X}}` 报「未知前缀」 | **通过（自动化）** | `protocol.TestParseAcceptsAllNineProviderKeys`、`TestParseRejectsOldUnqualifiedProviderNames`、`TestParseRejectsCredPrefix`、`TestProviderKeysAreEndpointQualified`（顺序逐字符锁死）；前端 `placeholder.test.ts` 同一组例子 |
| 5 | 引用 `{{provider.openai.*}}` + 绑定的 provider 没配 openai 端点 → 发布校验 `endpoint_missing`，**阻断** | **通过（自动化）** | `configsets.TestValidateEndpointMissingIsBlocking`（`warning == false`，`path` 是 `.codex/config.toml`，文案含「OpenAI 端点」）、`TestValidateNoEndpointMissingWhenBothConfigured`、`TestEndpointMissingIsReportedOncePerEndpoint` |
| 6 | `auth_field_mismatch` 与 `FixAuthField` 在 `{{provider.claude.auth_token}}` 下仍然工作，且只管 claude 端点 | **通过（自动化）** | `configsets.TestValidateAuthFieldMismatchGivesFix`、`TestFixAuthFieldRewritesDraft`（只改键名，base_url 行不动）、`TestAuthFieldMismatchIgnoresOldUnqualifiedToken`（旧字面量报语法错，不报 mismatch） |
| 7 | 快照裁剪：只发 `refs.provider_keys` 里出现过的键；`openai.model` 取 provider 的 `default_model` | **通过（自动化）** | `configsync.TestProviderValuesMapsAllNineKeys`（九个键一次全对）、`TestSnapshotOmitsUnreferencedProviderKeys`（`NotContains claude.auth_token`）、`TestOpenAIModelComesFromProviderDefault`（binding 四槽是 glm-5.2，快照给 glm-4.7）、`TestProviderValuesUsesPerEndpointKeys` |
| 8 | 端点缺失走到 configsync → `ErrEndpointMissing`，指派置 `failed` 且**不置** `degraded` | **通过（自动化）** | `configsync.TestProviderValuesRejectsUnconfiguredEndpoint`、`TestPullMarksAssignmentFailedOnEndpointMissing`（`state == failed`、显式 `NotEqual degraded`、`last_error` 含 `openai`、一条快照都没下发） |
| 9 | `claude.auth_token` 与 `openai.api_key` **都**落进秘密档；其余七个进尽力档 | **通过（自动化）** | `render.TestBothEndpointKeysAreSecrets`、`TestUnrestorableOpenAIKeyMakesUnsafe`（`Safe == false`）、`TestSevenNonSecretProviderKeysAreBestEffort`；安全底线用例 `TestSecurityRestoredContentNeverContainsSecretValues` 对两个键都断言 |
| 10 | 两个端点 key 相同时，还原后留下的是 `ProviderKeys` 里靠前的那个 | **通过（自动化）** | `render.TestSameKeyOnBothEndpointsKeepsClaude`（留下 `{{provider.claude.auth_token}}`）、`TestSameValueKeepsEarlierProviderKey`（四槽同值留 `claude.model`） |
| 11 | 库里有 provider、主密钥不匹配 → 拒绝启动，错误信息含「服务配置 X 的 claude 端点」与备份提示 | **通过（自动化）/ 待真机目视** | `hub.TestServeFailsWhenProviderKeyDoesNotDecrypt`（OnServe 失败，错误串含平台名、「claude 端点」、`secret.key`、`ORCINY_SECRET_KEY`）；`providers.TestVerifyAllRejectsWrongMasterKey`、`TestVerifyAllNamesTheFailingEndpoint`。**真实备份恢复到新机器**的场景未在真机上演练 |
| 12 | 在含「provider + 被它引用的凭据」的库上跑 `004` + `005`：密文搬运后仍解得开，旧字段与 `credentials` collection 消失，`down005` 返回错误 | **通过（自动化）** | `migrations.TestMigration004MovesCipherIntoProvider`（在重建的 M1.5 形状上真搬一次、真解一次密）、`TestMigration005DropsLegacyFieldsAndCollection`、`TestMigration005KeepsEndpointFields`、`TestCipherSurvivesFullMigrationChain`、`TestDown005Refuses`。另在**真实备份副本**上跑通全链，见下节 |
| 13 | 改 provider 的任一端点 → 重注入全机队、**不产生新 Revision** | **通过（自动化）** | `testsupport.TestProviderBindingEndToEnd`（改 base_url 后落盘变了、head 与 checksum 不变、`agent.LocalDrift` 为空）；`configsync.TestNotifyProviderDoesNotCreateRevision`、`TestRotatingProviderKeyReinjectsWithoutNewRevision`（换 key 同样不产生新版本，通知不带 RevisionID、reason 为 rotated） |
| 14 | 凭据页、`/credentials` 三个路由、`{{cred.*}}` 词法、`ConfigSnapshot.Credentials`、`secrets.json` 的 `creds` 全部消失；机器变量编辑器**保留** | **通过（自动化）** | `routes.TestCredentialRoutesAreGone`（三条路由 404）；`protocol.TestConfigSnapshotHasNoCredentialsField`（含「键 6 不许被占用」）、`TestParseRejectsCredPrefix`；`secrets.TestFileHasNoCredsField`、`TestSaveWritesNoCredsKey`、`TestLoadIgnoresLegacyCredsKey`；前端 `Sidebar.test.tsx`（无「凭据」入口）。`VariablesEditor` 与 `PUT /machines/{id}/variables` 原样保留，`variables` 包有独立用例 |
| 15 | 导入向导与漂移的「抽取」都写进 provider 的端点 key，并把文件里**每一处**该值替成对应端点的占位符 | **通过（自动化）** | `importer.TestExtractReplacesValueWithPlaceholder`（key 进端点、末四位回填、草稿只剩占位符）、`TestExtractReplacesEveryOccurrence`（两处都替）、`TestExtractIntoOpenAIEndpointUsesAPIKeyToken`（openai 侧写 `{{provider.openai.api_key}}`）、`TestExtractRejectsShortValue`；`drift.TestExtractKeyWritesIntoProviderEndpoint` |
| 16 | `ProviderDialog` 选预设带出两组端点；端点级 key 留空则不覆盖；编辑态密码框为空且提示「留空则不修改」 | **通过（自动化）/ 待真机目视** | 前端 `ProviderDialog.test.tsx`：「选预设时两个端点一起带出」、「编辑态保存时不带 key 字段」（断言 `'key' in body === false`）、「填了端点级 key 就带上它」、「点「清除」把端点级 key 显式清空」、「编辑态密码框为空，提示留空则不修改，右侧显示末四位」 |
| 17 | `BindingBar` 里没配 claude 端点的 provider 置灰，悬停说明原因 | **通过（自动化）/ 待真机目视** | 前端 `BindingBar.test.tsx`「没配 claude 端点的 provider 在下拉里置灰」（`toBeDisabled` + `title` 含「还没有 Claude 端点」）、「模型下拉取 claude 端点的模型清单」 |
| 18 | `go test -tags=testing ./...` 与 `npm test` 全绿；`make lint` 通过 | **通过** | 见下节「全量测试」 |
| 19 | 二进制体积仍在 M1.5 量级（agent < 30 MB、hub < 64 MB） | **通过** | agent **7.9 MB**、hub **25 MB**（与 M1.5 验收同口径、同数量级，未见增长） |

---

## 全量测试

```text
$ go test -tags=testing -count=1 ./...
# 37 个包 ok，690 个 --- PASS（含子测试），0 FAIL

$ cd hub/internal/site && npm test
 Test Files  23 passed (23)
      Tests  126 passed (126)

$ make lint
go vet ./...        # 无输出 = 通过

$ cd hub/internal/site && npm run compile
# 0 missing（zh 321 条为源语言，en 321 条全部有译文）

$ make build && ls -lh dist/orciny dist/orciny-agent
-rwxr-xr-x  25M  dist/orciny
-rwxr-xr-x  7.9M dist/orciny-agent
```

对比 M1.5 验收：36 包 / 624 PASS / 前端 19 文件 101 用例
→ M1.6 为 **37 包 / 690 PASS / 前端 23 文件 126 用例**。
新增的一个包是 `hub/internal/variables`（`secretbox` 顶替了被删掉的
`hub/internal/credentials`，净 +1）。

### 迁移链在真实备份上的验证

在 `supplemental/docker/pb_data-backup-20260822-054948.tar.gz` 的**副本**上
（解压到 `/tmp`，验证完即删）跑了一次真实启动：

```text
$ ./dist/orciny serve --dir /tmp/orciny-migrate-check/pb_data --http 127.0.0.1:18090
2026/08/22 21:45:49 Server started at http://127.0.0.1:18090
```

hub 正常起来 = `providers.VerifyAll` 通过。迁移后的库：

- `credentials` collection 已消失（迁移前手工塞过一行凭据记录，确认 `005`
  能删掉非空的 collection）
- `providers` 表的列恰好是新形状：`name / preset / note / key_cipher /
  key_last4 / claude_key_cipher / openai_key_cipher / claude / openai /
  created / updated`，五个旧字段（`base_url` / `auth_field` / `credential` /
  `models` / `defaults`）都不在
- `idx_providers_name` 唯一索引仍在

**一处要如实说明：** 这份备份是 2026-08-22 早上取的，当时库还停在迁移 `002`
（没有 `providers` collection，`credentials` 表是空的）。因此这次真实数据上的
运行**没有实际搬运过任何密文**——`003` / `004` / `005` 是一次跑完的，
`004` 的 backfill 面对的是一张空表。密文搬运那条路径由
`migrations.TestMigration004MovesCipherIntoProvider` 在**重建出来的 M1.5 形状**上
覆盖（见下节修正 3）：真建凭据、真加密、跑搬运、再真解一次密。

---

## 实现过程中相对计划的修正

计划的十个子计划整体可执行，绝大多数任务照做即可。以下六处是执行时发现并
修正的，每一处都补了测试或注释锁住。

1. **`004` 还必须把顶层 `base_url` 也转成非必填**（子计划 03 Task 1）。
   00-overview 的「偏离一」已经想到 `credential` relation 的 `Required` 要撤，
   但漏了 `003` 建的 `base_url` 同样是 `Required: true`。它在 `004` 之后就没有
   写入方了（值搬进 `claude` 子结构），留着必填会让 `004` 与 `005` 之间新建的
   每一条 provider 都存不进去——`providers.Store` 的全部 Create 用例都会红。
   理由与 `credential` 那条一字不差，因此并进同一段处理，`down004` 也对称地
   还原。锁在 `migrations.TestProviderEndpointFieldsExist`。

2. **端点级 key 清空之后，末四位回落到平台级那把，不是清空**（子计划 03 Task 4）。
   计划的 `TestUpdateKeyTriState` 断言清空端点级 key 后
   `providers.ClaudeOf(r).KeyLast4` 为空，但计划自己给出的 `stampLast4` 实现是
   「末四位跟着**实际生效的那把 key** 走」——清掉端点级之后生效的是平台级，
   末四位理应变成平台级那把的末四位。计划的两处自相矛盾。
   取实现那一侧：它与同一份计划里的 `TestEndpointKeyOverridesPlatformKey`
   （openai 没设端点级 key，末四位断言为平台级的 `3456`）一致，
   语义上也是对的——UI 上那行 `····xxxx` 应当告诉用户「这个端点实际在用哪把 key」。
   测试改成断言 `3456`，并写明理由。

3. **`004` 的搬运测试要先把 M1.5 的形状重建出来**（子计划 03 Task 1 / 08 Task 1）。
   计划让测试在 `newApp(t)` 上直接 `seedCredentialWith` + `seedLegacyProvider`，
   但 `tests.NewTestApp` 会把**全部**迁移跑完——`005` 跑完之后 `credentials`
   collection 与五个旧字段都没了，这两个 seed 函数无处落脚。
   加了一个 `restoreLegacyShape` helper：在跑完全链的库上重新建出
   `credentials` collection 与 providers 的五个旧字段，再 seed、再跑
   `BackfillProviderEndpoints`。这样「密文搬运」这条本期最容易造成数据面损坏的
   路径仍然有真搬一次、真解一次密的覆盖。同理，`003` 与 `002` 的几条断言里
   针对已被 `005` 删掉的字段/collection 的部分改写成「归 005 的用例管」，
   而不是删掉断言。

4. **`ProviderDialog` 的端点分区要在选预设时重挂**（子计划 09 Task 4）。
   计划让折叠状态用 `useState(() => endpoint.base_url !== '')` 初始化。但那只在
   首次挂载时算一次：新建对话框打开时两个端点都是空的 → 都折叠；之后点「智谱
   GLM」把 base_url 填进去，分区**仍然是折叠的**，计划自己的两条测试
   （断言能读到 `claude base_url` / `openai base_url` 的值）因此失败。
   改法是给两个 `<EndpointSection>` 挂 `key={\`claude-${preset}\`}`：
   选预设本来就是「把这一侧整个换掉」，跟着重挂、按新的 base_url 重算折叠状态
   语义正好。没有引入 `useEffect` 同步 state——那会让「用户手动折叠过」这个
   意图被后续 render 覆盖掉。

5. **`agent/internal/applier` 的 `modeOf` 没有出现在任何子计划里**。
   它按「文件里含 `{{cred.*}}` 引用则 0600」推导历史数据的权限位（M1 spec §7.4），
   `protocol.RefCred` 一删就编译不过。判据平移成「含两个端点的 key 引用则 0600」
   ——那正是 M1.6 之后「秘密引用」的定义（spec §3.4）。

6. **`configsets.NewService` 内部自建的 `providers.Store` 传 `nil` 主密钥**
   （子计划 04）。`providers.NewStore` 多了一个 `key []byte` 参数，而 configsets
   只读 provider 的**非秘密**部分（端点配没配、`auth_field`），从不解密。
   传 `nil` 而不是把主密钥一路穿进来：给它一把钥匙只会扩大明文可及的范围。
   写在构造函数的注释里。

**预设表没有出现数据错误。** M1.5 那轮在这里栽过三处，本轮 claude 侧七条
按计划要求原样搬入（一个值都没改），openai 侧按计划给出的表逐条填入。
但要如实说明：**openai 侧的 base_url 与模型 id 没有在线核过各平台文档**
（本次执行没有联网），填的是计划表里的值加上「同平台 claude 侧模型去掉
`[1m]` 一类 Claude Code 侧后缀」的推导。ZenMux 的 openai 侧按它的
`厂商/模型` 全 id 约定重排了一遍。这是一处**待核实项**，见下节。

**关于「迁移拆成 004 + 005 之后每一步是否都绿」**（00-overview 偏离一的赌注）：
成立，但需要修正 1 才成立。加上 `base_url` 转非必填之后，`004` 落地时
`go test -tags=testing ./hub/internal/providers/ ./hub/internal/migrations/`
确实是绿的；整棵树的绿要等到 `08` 删完 credentials 包（这与计划的预期一致，
计划在 08 Task 2 Step 4 明写「这是 M1.6 后端第一次整棵树通过」）。

---

## 待真机验收

以下条目只有自动化覆盖，没有在多台真机或浏览器里目视过：

| 条目 | 自动化覆盖 | 缺什么 |
|---|---|---|
| DoD 11：主密钥不匹配拒绝启动 | `hub.TestServeFailsWhenProviderKeyDoesNotDecrypt` 在临时库上换掉 `ORCINY_SECRET_KEY` 触发 | **真实备份恢复到新机器**的完整场景（带 `pb_data` 拷贝、不带 `secret.key`），以及错误信息在实际终端输出里的可读性 |
| DoD 13：改 provider 重注入全机队 | `testsupport.TestProviderBindingEndToEnd` 用一台进程内 agent 走完整链路 | ≥2 台真机下的收敛时延与并发行为 |
| DoD 3 / 16 / 17：三处 UI | `Providers.test.tsx` / `ProviderDialog.test.tsx` / `BindingBar.test.tsx` 断言 DOM 结构与文案 | 页面观感：一条两行的密度是否可读、置灰的「未配置」是否够显眼、端点分区折叠后信息是否够用、悬停 title 在真实浏览器里是否出得来 |
| DoD 15：抽取向导 | `importer` / `drift` 的单测覆盖服务端；`ExtractDialog.test.tsx` 覆盖对话框 | 从导入向导第三步点「抽成服务配置的 key」到反查预选到抽取完成的整条交互 |
| 预设表 openai 侧数据 | `providers.TestPresetOpenAIEndpointIsCompleteWhenPresent`（结构自洽：填了就填齐、默认模型在清单里、不带四槽）、`TestVolcengineHasBothEndpointsOnDifferentPaths` | **各平台 2026-08 文档的在线核对**。本次执行无网络，openai 侧的 base_url 与模型 id 未逐条核实——见上节最后一段。M1.5 在这里出过三次错，建议联网后过一遍再上真机 |
