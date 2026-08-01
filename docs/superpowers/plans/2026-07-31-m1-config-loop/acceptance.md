# M1 验收记录

对照 [spec §12](../../specs/2026-07-31-m1-config-loop-design.md) 的十五条 DoD，
以及子计划 17 的收尾清单。

**执行日期：** 2026-08-01  
**执行环境：** macOS（Darwin arm64）· Go 1.26 · Node（Vitest 4）  
**分支：** `m1/cli-and-tails` @ `d715af9`  
**说明：** 本轮**没有**三台异构真机。可在单机用集成测试与 CLI 冒烟如实覆盖的
条目已测；需要多机联调的条目标为**待真机验收**并写清对应的自动化覆盖。

---

## 总表

| # | 标准 | 结论 | 依据 |
|---|---|---|---|
| 1 | 导入向导 → 配置集 v1；敏感项抽成凭据；blob 无明文 | **通过（自动化）** | `hub/internal/importer` + 集成接线；前端导入向导 `npm test` |
| 2 | 第二台机器 apply 指派 → 落盘 → 内容一致 | **通过（自动化）** | `syncer`：`TestBlobDataCompletesApply` 等；`configsync` 回执链路 |
| 3 | 第三台机器 survey 指派 → 不写盘，差异进收件箱 | **通过（自动化）/ 待真机目视** | `TestSurveyModeDoesNotWrite`、`TestSurveyReportsDifferencesWithoutWriting`、`TestSnapshotSurveyMode` |
| 4 | 新增 skill → 收件箱 → 收编 → 其余机器落盘 | **通过（自动化）/ 待真机计时** | watcher 扫描 + drift 收编集成；真机「< 60 秒」未掐表 |
| 5 | 改 CLAUDE.md → diff → 恢复 → 回基线 | **通过（自动化）** | `TestDriftCommandRestoreRewritesFromBaseline`；hub unified diff |
| 6 | 改密钥值 → 上报内容不含明文 | **通过（自动化）** | `TestSecurity*`（render）；drift CLI 脱敏 `TestDriftCommandOutputIsRedacted` |
| 7 | `.claude.json` 非受管键保留且不重排 | **通过（自动化）** | applier `keys` 合并单测 |
| 8 | apply 失败自动回滚 + 面板显示原因 | **通过（自动化）** | applier 回滚单测；`TestApplyAckFailureAndDegraded`；前端 degraded UI |
| 9 | 凭据轮换不产生新 Revision、不产生漂移 | **通过（自动化）** | `TestNotifyCredentialSendsRotatedNotify`；rendered hash 口径 |
| 10 | 发布 v3 → 回滚 v1 → 生成 v4 → 全机队对齐 | **通过（自动化）** | `revisions` 回滚 + `configsync` 通知 |
| 11 | 跨机器同路径冲突强制三方对比 | **通过（自动化）/ 待真机目视** | drift `ErrConflict`；前端三方对比组件测试 |
| 12 | 删 `state.json` → survey 全量对账，不覆盖用户文件 | **通过（自动化）** | `TestPullWithLostStateFallsBackToSurvey` |
| 13 | `go test -tags=testing ./...` 与 `vitest run` 全绿 | **通过** | 见下「全量测试」 |
| 14 | agent < 30 MB、hub < 64 MB（RAM） | **部分（体积已测，RSS 沿用 M0）** | 二进制：agent **7.9 MB**、hub **25 MB**；RSS 见 M0 验收（agent 13.6 MB / hub 34.8 MiB），M1 增量未重测常驻 RSS |
| 15 | M0 尾巴两项 | **通过** | ① `hub.pub` 垃圾 → Compromised（`TestCorruptHubKeyEntersCompromised`）；② DoD 第 5 条改为 75 秒（文档） |

---

## 全量测试

```text
$ go test -tags=testing ./...
# 35 个包 ok，485 个 --- PASS（含子测试），0 FAIL

$ go test -tags=testing -run TestSecurity -v ./agent/...
TestSecurityRestoredContentNeverContainsCredentialValues  PASS
TestSecurityUnsafeRestoreIsReported                       PASS
TestSecurityRestoreHandlesPathologicalInput               PASS

$ cd hub/internal/site && npm test
 Test Files  11 passed (11)
      Tests  55 passed (55)

$ make lint
go vet ./...   # 通过

$ make build-agent build-hub && ls -lh dist/
orciny         25M
orciny-agent  7.9M
```

### CLI 冒烟

```text
$ orciny-agent --help
Available Commands:
  drift    查看本机相对基线的漂移（离线，含简易 diff）
  pause    暂停本机配置管理
  resume   恢复本机配置管理
  status   查看连接状态、配置版本、健康与漂移
  sync     立即从 hub 拉取并应用当前配置
  ...
```

---

## 子计划 17 交付核对

| 任务 | 提交 | 状态 |
|---|---|---|
| 1 `sync` / `status` | `c859438` | 完成 |
| 2 `drift` / `pause` / `resume` | `8c59f0f` | 完成 |
| 3 `hub.pub` → Compromised | `81376c7` | 完成 |
| 4 M0 DoD 第 5 条 → 75 秒 | `6b32a10` | 完成 |
| 5 运维文档 M1 | `4e86a47` | 完成 |
| 6 本验收记录 | （本文件） | 完成 |
| 测试稳住（watcher/probe） | `38407b9` | 完成 |

---

## 待真机补测

| 项 | 怎么补 |
|---|---|
| DoD 3 目视 | 第三台 survey 指派 → `ls ~/.claude` 无改动 → 面板收件箱有差异 |
| DoD 4 计时 | 任一台 `mkdir -p ~/.claude/skills/foo && echo x > .../SKILL.md`，掐表到收件箱 |
| DoD 6 查库 | 改密钥后 `sqlite3 pb_data/data.db` 扫 blobs，确认无明文 |
| DoD 11 目视 | 两台改同一文件 → 收编按钮禁用 → 打开三方对比 |
| DoD 12 服务重启 | `rm state.json && systemctl restart orciny-agent`，确认转 survey |
| DoD 14 RSS | `ps -o rss= -p $(pgrep orciny-agent)` 与 `docker stats` 记峰值 |
| DoD 15 真机 | 把随机 32 字节写入 `hub.pub`，确认 agent 日志 Compromised 且停重试 |

---

## 实现期修正（相对计划正文）

| # | 计划原文 | 实际做法 | 理由 |
|---|---|---|---|
| 1 | watcher 去抖测试「Notify 五次再 Advance 一次」 | `waitReport`：Advance 后若无上报且 debounce 仍在册则再推 2s | 假时钟下 `debounceC` 与 `manual` 可同时就绪，select 先走 manual 会丢弃已到期滴答 |
| 2 | probe 测试用 PATH 假 claude 脚本 | 解析类用例注入 `runClaudeVersion`；超时用例仍真 fork | 全量并行时 fork 壳脚本会被调度延迟拖过 3s 探测超时 |
| 3 | `Status` 结构体追加配置字段 | 配置字段从 `state.json` 现读打印，不写入 `status.json` | `status.json` 仍是连接态；配置态属于 `state.json`，混写会让两个文件职责糊在一起 |

---

## 与 M0 尾巴的闭环

| M0 待办 | M1 处置 |
|---|---|
| `hub.pub` 无法解析时无限重试 | **已修** `81376c7`：`identity.ErrHubKeyUnusable` → `StateCompromised` |
| DoD 第 5 条 60 秒 → 75 秒 | **已修** `6b32a10`：overview / acceptance / packaging 同步 |
