# Orciny 运维手册（M0）

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

> **必须知道的一条：** hub 的私钥 `pb_data/orciny_hub_key.pem` 也在数据目录里，
> 因此**会被备份一起带走**。反过来说——
>
> **恢复备份时若丢了这个文件，所有 agent 都会因 hub 签名验证失败而拒绝连接，
> 需要在每台机器上重新 enroll。**
>
> agent 侧的表现是日志里出现「hub 签名验证失败，停止一切重试」，且进程不再
> 重连（这是有意的：签名不匹配意味着要么遭中间人，要么 hub 换了密钥，
> 两种都需要人来判断）。
>
> 恢复演练时请务必确认这个文件在备份里，并在恢复后用 `orciny-agent status`
> 抽查一台机器。

## 机器状态怎么读

| 状态 | 含义 |
|---|---|
| 在线 | WebSocket 长连接存续。面板不显示秒级心跳时间——`last_seen` 只在状态变化时写库 |
| 离线 | 连接断开超过 5 秒宽限。`last_seen` 正是断开那一刻 |
| 已暂停 | M1 起才有语义（保持连接但不应用下发）。M0 不提供切换入口 |

短暂的网络抖动（5 秒内恢复）不会让面板闪红，也不会产生断连事件。

## 常见排查

**机器一直不出现**

在目标机器上：

```bash
orciny-agent status            # 看 hub 地址、指纹、连接状态
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

`code=3` 有一个容易踩的变体：agent 版本形如 `0.1.0-SNAPSHOT-abc1234` 时，
按 semver 它**低于** `0.1.0`（带预发布标识的版本排在同号正式版之前），
因此快照构建连不上门槛为 `0.1.0` 的 hub。这是正确行为，不是缺陷——
要连就用正式版产物。

**重装系统或删掉了 `~/.orciny/identity/`**

指纹由公钥派生，密钥没了就是一台新机器。重新执行一次安装命令即可，
旧记录可以在面板上删掉。

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
