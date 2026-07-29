# Orciny

面向个人与微型团队的自托管 Coding Agent 机队中台——集中管理多台机器上的 `~/.claude`，聚合 Token 用量与订阅余额。

> beszel 之于服务器监控，Orciny 之于 Coding Agent 管理。

## 分支约定

- **`genesis`**（当前默认）— 保留从 0 开始的构思、设计与原型记录，是完整的开发史。
- **`main`** — 待项目具备可用代码后启用，作为发布分支，只保留干净的可用实现。

## 内容

| 路径 | 说明 |
|---|---|
| [docs/PRODUCT-DESIGN.md](docs/PRODUCT-DESIGN.md) | 产品设计文档（正本）— 架构、功能规格、数据模型、协议对齐、安全、里程碑与路线图 |
| [docs/product-design.html](docs/product-design.html) | 产品设计文档的 HTML 排版版（同内容） |
| [docs/concept-report.html](docs/concept-report.html) | 项目构思报告 — 需求验证、竞品格局、七组选型论证、命名提案 |
| [mock/ui-mock.html](mock/ui-mock.html) | hub Web UI 的可交互静态原型（单文件，暖黑/暖纸双主题） |
| [docs/superpowers/specs/](docs/superpowers/specs/) | 工程设计 spec（按里程碑分册，可直接转为实现计划） |

## 选型基线（v0.1）

独立项目（协议与 leycode 对齐） · Go + PocketBase hub + Go agent · 中台为准 + 漂移收编 · 会话 JSONL 用量采集 · ai-plan-insight 全量 Go 重写 · agent 外拨 WebSocket · MIT 开源。

详见 [产品设计文档](docs/PRODUCT-DESIGN.md)。

## 状态

设计阶段。产品设计 v0.1 已定稿；M0（骨架）的工程设计已完成，见
[docs/superpowers/specs/2026-07-28-m0-skeleton-design.md](docs/superpowers/specs/2026-07-28-m0-skeleton-design.md)。
尚无实现代码。
