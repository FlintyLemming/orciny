# Orciny 运维手册（M0 + M1）

## 部署 hub

```bash
mkdir orciny && cd orciny
curl -fsSLO https://raw.githubusercontent.com/FlintyLemming/orciny/main/supplemental/docker/docker-compose.yml
docker compose up -d
docker compose exec orciny orciny superuser create you@example.com '一个足够长的密码' --dir=/pb_data
```

hub 假定跑在反向代理之后。**不要把明文 HTTP 暴露到公网**：agent 的信任根
建立在 enroll 那一刻的 HTTPS 之上，明文等于把首次接入交给中间人。

Nginx 需要显式透传 WebSocket，且 `proxy_read_timeout` 要大于 hub 的 70 秒
读超时；Caddy 默认即可。

## 下载源（agent 二进制分发）

`GET /install.sh` 注入的下载源默认是 `https://github-dl.flinty.moe/v<version>`：一个
Cloudflare Worker 反代 GitHub Release 并做边缘缓存，客户端全程只连
`github-dl.flinty.moe`，不再直连 GitHub，中国大陆下载不再卡。脚本同时把 GitHub 直连
作为兜底，Worker 故障时自动回退。

Worker 脚本在 `supplemental/cloudflare/dl-worker.js`，已部署为账号下的
`dl-orciny` Worker，绑定到 `github-dl.flinty.moe`（Custom Domain，CF 自动管 DNS 与
证书）。Worker 匿名即可，GitHub 公开 release 不需要凭据。

改 Worker：

```bash
# 用 Cloudflare API 重新上传（需要 Workers 编辑权限的 API token + account id）
curl -X PUT \
  "https://api.cloudflare.com/client/v4/accounts/<account_id>/workers/scripts/dl-orciny" \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: multipart/form-data; boundary=BOUNDARY" \
  --data-binary @body.txt   # body 见 supplemental/cloudflare/ 的部署说明
```

或在 dashboard: Workers & Pages -> `dl-orciny` -> 编辑 -> 部署。

产物路径形如 `releases/download/v<version>/<archive>`，归档名由 `.goreleaser.yml`
的 `name_template` 决定，改它必须同步改 `install-agent.sh` 的 `ARCHIVE=` 那行。

## 接入机器

面板 →「机器」→「添加机器」，把弹出的一行命令贴到目标机器上执行。token
15 分钟有效、只能用一次。

谨慎的做法是核对 `--hub-key`：它是 hub 的公钥指纹，显示在「设置」页。
指纹对不上时脚本会中止安装，不会钉扎错误的公钥。

安装脚本以**目标用户**身份运行 agent，不是专用系统用户——M1 起 agent 要
读写 `~/.claude`，属主必须正确。因此**不要以 root 执行**安装命令；用目标
用户执行（需要时脚本自己会调 `sudo`）。带 `sudo` 跑也可以，脚本认
`SUDO_USER`。

重复执行同一条安装命令是安全的：脚本会覆盖二进制、重新 enroll（记
`machine.re-enrolled`）并重启服务。升级 agent 走的就是这条路径。

## 备份与恢复

备份走 PocketBase 自带机制（面板「设置」→「打开备份设置」，或 `/_/#/settings/backups`），
支持本地目录与 S3 兼容存储。

> **必须知道的两条密钥：**
>
> 1. hub 的私钥 `pb_data/orciny_hub_key.pem` 也在数据目录里，因此**会被备份一起带走**。
>    恢复备份时若丢了这个文件，所有 agent 都会因 hub 签名验证失败而拒绝连接，
>    需要在每台机器上重新 enroll。
>
> 2. **主密钥** `pb_data/secret.key`（M1）必须与数据库一起备份。它用来加解密
>    凭据集合。恢复到新机器时忘了带它，hub 会**拒绝启动**并给出明确错误——
>    这是有意的（spec §6.6）：解不开凭据比带着坏数据继续跑更安全。
>    也可以用 `ORCINY_SECRET_KEY` 环境变量注入（优先于文件）。
>
> agent 侧的表现是日志里出现「hub 签名验证失败，停止一切重试」，且进程不再
> 重连（这是有意的：签名不匹配意味着要么遭中间人，要么 hub 换了密钥，
> 两种都需要人来判断）。
>
> 恢复演练时请务必确认这两个文件在备份里，并在恢复后用 `orciny-agent status`
> 抽查一台机器。

## agent 的两个 HOME

| 名字 | 默认 | 用途 |
|---|---|---|
| `$ORCINY_HOME`（agent 目录） | `~/.orciny` | agent 自己的数据：身份、状态、日志、blob 缓存、快照 |
| `managed_home`（`agent.yml`） | `os.UserHomeDir()` | **被管理的**那个 home，即 `~/.claude` 所在处 |

两者必须分开：测试与容器部署都靠它隔离。容器里跑 agent 时，`managed_home`
要显式指定到挂载了用户配置的路径，否则 agent 会去管容器自己的空 home。

```yaml
# ~/.orciny/agent.yml
hub_url: https://orciny.example.com
machine_id: ...
managed_home: /home/alice   # 容器里显式指定
reconcile_interval: 5m      # 定时全量对账，默认 5 分钟
```

## 快照与磁盘占用

| 路径 | 内容 | 可否删 |
|---|---|---|
| `~/.orciny/snapshots/` | 最近 **5** 次 apply 前的现场（目录 0700，文件 0600） | 可以。代价是丢掉回滚现场 |
| `~/.orciny/blobs/` | 内容寻址缓存（渲染前的 blob） | 可以。下次 apply 会重新从 hub 拉 |
| `~/.orciny/secrets.json` | 本机凭据/变量缓存（0600） | **不要删**——离线还原与对账依赖它 |
| `~/.orciny/state.json` | 当前已应用的版本与基线（0600） | 删了会触发 survey 全量对账，**不会**覆盖用户文件 |

## `degraded` 怎么办

`degraded` 表示：**一次 apply 失败，且回滚也失败**。此后 agent 不再自动 apply
任何后续版本——继续写只会把现场破坏得更彻底。

排查步骤：

1. 看 `~/.orciny/logs/`（或 `journalctl -u orciny-agent`）里 apply 失败的原因；
2. 看 `~/.orciny/snapshots/` 里最近一次快照，对照用户目录确认哪些文件处于中间态；
3. 手工把用户目录恢复到可接受的状态；
4. 在面板机器详情点「解除 degraded」——agent 会转 survey 模式做全量对账，
   差异进收件箱，由人决定收编还是恢复，**不会**静默覆盖用户文件。

## 机器状态怎么读

| 状态 | 含义 |
|---|---|
| 在线 | WebSocket 长连接存续。面板不显示秒级心跳时间——`last_seen` 只在状态变化时写库 |
| 离线 | 连接断开超过 5 秒宽限。`last_seen` 正是断开那一刻。静默断网最坏 **75 秒内**转 offline（70s 读超时 + 5s 宽限） |
| 已暂停 | 本机执行了 `orciny-agent pause`：保持连接但不 apply、不上报漂移 |

短暂的网络抖动（5 秒内恢复）不会让面板闪红，也不会产生断连事件。

指派状态（配置闭环）：

| 状态 | 含义 |
|---|---|
| `pending` | 已指派，等待 agent 拉取 |
| `applying` | agent 正在 apply |
| `aligned` | 与 head 一致 |
| `failed` | apply 失败（已回滚） |
| `degraded` | apply 失败且回滚也失败，需人工介入 |
| `paused` | 配置集或本机暂停 |

## CLI 速查

| 命令 | 用途 | 副作用 |
|---|---|---|
| `orciny-agent status` | 连接状态、配置版本、健康、本机漂移数 | 无（只读；漂移数会做一次本地扫描） |
| `orciny-agent sync` | 立即 pull + apply，打印计划与结果 | **会短暂顶掉常驻 agent 的连接**（几秒内自动重连） |
| `orciny-agent drift` | 本机离线对账 + 简易 diff（已脱敏） | 无（只读、不联网）。收编/恢复请在 Web 上操作 |
| `orciny-agent pause` | 暂停本机配置管理 | 写 `state.json`；下次握手经 `LocalPaused` 上报面板 |
| `orciny-agent resume` | 恢复本机配置管理 | 同上 |
| `orciny-agent enroll` | 用一次性 token 接入 hub | 写身份与 `agent.yml` |
| `orciny-agent run` | 前台运行（服务单元调用的就是它） | 长连接 + 监视 + 对账 |

## 常见排查

**机器一直不出现**

在目标机器上：

```bash
orciny-agent status            # 看 hub 地址、指纹、连接状态、配置版本
journalctl -u orciny-agent -n 50          # Linux
tail -50 ~/.orciny/logs/launchd.err.log   # macOS
```

日志里的关键几种：

| 日志 | 含义与处置 |
|---|---|
| `hub 拒绝连接 code=1` | 指纹未登记。机器可能在面板上被删过，重新 enroll |
| `hub 拒绝连接 code=3` | agent 版本低于 hub 门槛，升级 agent |
| `hub 报告本机已被删除` | 面板上删掉了这台机器，需重新 enroll |
| `hub 签名验证失败，停止一切重试` | hub 换了密钥（多半是恢复备份时丢了私钥），或遭中间人。**不要**盲目删掉 `~/.orciny/identity/hub.pub`，先确认 hub 侧发生了什么 |
| `钉扎的 hub 公钥无法使用，停止一切重试` | `hub.pub` 文件损坏或被替换成垃圾。确认 hub 身份后重新 enroll |
| `本机处于 degraded 状态` | 见上文「degraded 怎么办」 |

`code=3` 有一个容易踩的变体：agent 版本形如 `0.1.0-SNAPSHOT-abc1234` 时，
按 semver 它**低于** `0.1.0`（带预发布标识的版本排在同号正式版之前），
因此快照构建连不上门槛为 `0.1.0` 的 hub。这是正确行为，不是缺陷——
要连就用正式版产物。

**重装系统或删掉了 `~/.orciny/identity/`**

指纹由公钥派生，密钥没了就是一台新机器。重新执行一次安装命令即可，
旧记录可以在面板上删掉。

**删掉了 `~/.orciny/state.json`**

agent 会向 hub 报告「我没有任何已应用的版本」。若这台机器曾经对齐过，
hub 会自动打回 survey 模式：差异进收件箱，**不会**用中台内容覆盖用户文件。

## macOS 的已知限制

LaunchAgent 随用户登录启动。**headless 的 Mac mini 在无人登录时 agent 不运行。**
这是 macOS 的固有特性，M0 不绕。两条出路：

1. 系统设置里开启自动登录（最省事，但等于机器无人值守时也保持登录态）；
2. 改用带 `UserName` 键的 LaunchDaemon 变体，装在 `/Library/LaunchDaemons/`：
   ```xml
   <key>UserName</key><string>你的用户名</string>
   ```
   这样开机即运行且属主正确，代价是拿不到用户登录会话的环境变量
   （`PATH` 要在 plist 里写全，`claude` 的探测可能需要绝对路径）。

## 卸载

Linux：

```bash
sudo systemctl disable --now orciny-agent
sudo rm -f /etc/systemd/system/orciny-agent.service /usr/local/bin/orciny-agent
rm -rf ~/.orciny
```

macOS：

```bash
launchctl unload ~/Library/LaunchAgents/moe.flinty.orciny-agent.plist
rm -f ~/Library/LaunchAgents/moe.flinty.orciny-agent.plist
sudo rm -f /usr/local/bin/orciny-agent
rm -rf ~/.orciny
```

删完记得在面板上把这台机器也删掉。

## 已知限制：`~/.claude.json` 的竞写

Claude Code 自己也在写 `~/.claude.json`（记录 project 状态等运行时数据），
并且它不使用原子写。orciny-agent 在合并受管键（默认只有 `mcpServers`）时
会做三件事缓解：读之前记 mtime + size、写之前再查一次、变了就重读重试
（最多 3 次），最终写入走「临时文件 + rename」。

**仍然存在的窗口**：Claude Code 在 agent 完成检查到 rename 之间的那一瞬间
重写该文件时，agent 的写入可能被覆盖。下一次对账（默认 ≤5 分钟）会发现
并重新应用。

## inotify watch 数

`skills/**` 递归监视通常占用几十个 watch，远低于 Linux 默认的 8192 上限
（`/proc/sys/fs/inotify/max_user_watches`）。若同一台机器上跑了多个大量占用
watch 的工具而报 `no space left on device`，调高该内核参数即可：

```bash
# 临时
sudo sysctl -w fs.inotify.max_user_watches=524288
# 持久
echo fs.inotify.max_user_watches=524288 | sudo tee /etc/sysctl.d/99-orciny.conf
```
