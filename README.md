# Orciny

面向个人与微型团队的自托管 Coding Agent 机队中台——集中管理多台机器上的 `~/.claude`，聚合 Token 用量与订阅余额。

> beszel 之于服务器监控，Orciny 之于 Coding Agent 管理。

**当前进度：M0（骨架）** — hub 可部署、agent 一行接入、面板实时显示机队在线状态。
里程碑与完整功能规格见 [产品设计文档](docs/PRODUCT-DESIGN.md)。

## 快速开始

**1. 起 hub**

```bash
mkdir orciny && cd orciny
curl -fsSLO https://raw.githubusercontent.com/FlintyLemming/orciny/main/supplemental/docker/docker-compose.yml
docker compose up -d
docker compose exec orciny orciny superuser create you@example.com '一个足够长的密码' --dir=/pb_data
```

生产部署请放在反向代理之后走 HTTPS —— agent 的信任根建立在首次接入的那一刻，
明文 HTTP 暴露到公网等于把它交给中间人。配置示例见
[运维手册](docs/operations.md)。

**2. 接入机器**

浏览器打开面板 →「机器」→「添加机器」，把弹出的一行命令贴到目标机器上执行：

```bash
curl -fsSL https://<你的 hub>/install.sh | sh -s -- \
  --hub https://<你的 hub> --token <一次性 token> --hub-key <公钥指纹>
```

token 15 分钟有效、只能用一次。几秒后机器会出现在列表里。

**3. 之后**

- 机器状态、事件流、删除与改名都在面板上。
- 排查、备份恢复、卸载见 [运维手册](docs/operations.md)。
- agent 在 M0 **不读写 `~/.claude`**，只探测 `claude --version`。

## 分支约定

- **`genesis`**（当前默认）— 保留从 0 开始的构思、设计与原型记录，是完整的开发史。
- **`main`** — 待项目具备可用代码后启用，作为发布分支，只保留干净的可用实现。

## 内容

| 路径 | 说明 |
|---|---|
| [docs/PRODUCT-DESIGN.md](docs/PRODUCT-DESIGN.md) | 产品设计文档（正本）— 架构、功能规格、数据模型、协议对齐、安全、里程碑与路线图 |
| [docs/operations.md](docs/operations.md) | 运维手册 — 部署、接入、备份恢复、排查、卸载 |
| [docs/product-design.html](docs/product-design.html) | 产品设计文档的 HTML 排版版（同内容） |
| [docs/concept-report.html](docs/concept-report.html) | 项目构思报告 — 需求验证、竞品格局、七组选型论证、命名提案 |
| [mock/ui-mock.html](mock/ui-mock.html) | hub Web UI 的可交互静态原型（单文件，暖黑/暖纸双主题） |
| [docs/superpowers/specs/](docs/superpowers/specs/) | 工程设计 spec（按里程碑分册，可直接转为实现计划） |

## 选型基线（v0.1）

独立项目（协议与 leycode 对齐） · Go + PocketBase hub + Go agent · 中台为准 + 漂移收编 · 会话 JSONL 用量采集 · ai-plan-insight 全量 Go 重写 · agent 外拨 WebSocket · MIT 开源。

详见 [产品设计文档](docs/PRODUCT-DESIGN.md)。

## 状态

产品设计 v0.1 已定稿。M0（骨架）实现完成：wire 协议、双向 Ed25519 信任根、
enroll 与握手、连接生命周期、前端面板、打包与分发。工程设计见
[docs/superpowers/specs/2026-07-28-m0-skeleton-design.md](docs/superpowers/specs/2026-07-28-m0-skeleton-design.md)，
真机验收记录见
[acceptance.md](docs/superpowers/plans/2026-07-28-m0-skeleton/acceptance.md)。

M1 起 agent 才会读写 `~/.claude`；M0 只做骨架与在线状态。
