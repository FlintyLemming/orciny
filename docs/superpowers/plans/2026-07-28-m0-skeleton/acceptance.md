# M0 验收记录

对照 [spec §13](../../specs/2026-07-28-m0-skeleton-design.md) 的十条 DoD。

**执行日期：** 2026-07-29
**执行环境：** macOS 26.4（Darwin 25.4.0, arm64, Mac mini）· Docker Desktop（containerd 镜像存储）· Go 1.26.5 · goreleaser 2 · systemd 257（Debian trixie 容器）

**关于环境的重要说明：** 本轮验收**没有** VPS + 三台真机。可在单机上如实测量的条目
（1、2、4、5、6、7、9、10）已用「hub 跑在 Docker 容器 + agent 跑在宿主 macOS 或
Linux 容器」的组合实测；需要多台异构真机才有意义的条目（3，以及 4 里「服务单元
自动重启」那半条）标为**待真机验收**并写清怎么补。

不为了让表格全绿而降低标准：第 5、8 条实测未达 DoD 原文，如实记在下面。

---

## 总表

| # | 标准 | 结论 | 实测 |
|---|---|---|---|
| 1 | `make build` 产出两个二进制，各 < 30MB | **通过** | `orciny` 23.6 MB、`orciny-agent` 7.4 MB |
| 2 | `docker compose up -d` 起 hub，浏览器可登录并看到空机器列表 | **通过** | 镜像未压缩 35.8 MB / 压缩 13.2 MB（< 40MB）；`/` 返回真实 UI，`machines.totalItems = 0` |
| 3 | 3 台真机各自一行接入，单台耗时 < 2 分钟 | **待真机验收** | Linux 容器（真 systemd）单台走通，安装耗时数秒；缺 macOS 与多机 |
| 4 | `kill -9` agent → 面板 5–10 秒内转 offline；服务自动重启 → 转回 online | **通过**（前半实测，后半待真机） | `kill -9` → offline **5.11 s**；手工重启 → online **0.30 s** |
| 5 | 断网 60 秒 → 转 offline；恢复 → 自动重连 | **部分通过** | 恢复重连 **0.31 s** ✅；但转 offline 用了 **66.3 s**（> 60 s），见下 |
| 6 | 重启 hub → 机器自动重连，无残留幽灵 online | **通过** | 重启后库里立即是 offline（无幽灵），**1.46 s** 后重连回 online |
| 7 | 篡改 `hub.pub` → 拒绝连接、日志明确报出、不再重试 | **通过** | 进入 `compromised` 终态，进程退出，20 s 内零重试 |
| 8 | UI 删除机器 → 该 agent 收到明确原因并停止重试 | **首测未通过 → 已修，复测通过** | 修前：转 5 分钟固定间隔无限重试。修后：报「停止重试」并退出，15 s 零重试 |
| 9 | `go test -tags=testing ./...` 全绿 | **通过** | 203 个顶层用例（含子测试 213），全绿，`real 7.37s` |
| 10 | hub < 64MB RAM，agent < 30MB RAM，agent 空闲 CPU ≈ 0 | **通过** | hub **34.84 MiB** / CPU 0.02%；agent **13.6 MB** RSS / CPU **0.0%** |

---

## 逐条明细

### 1. 二进制体积

```
$ make build && ls -l dist/orciny dist/orciny-agent
dist/orciny        23.6 MB
dist/orciny-agent   7.4 MB
```

goreleaser 的六个交叉产物同样全部达标：hub linux/amd64 24 MB、linux/arm64 23 MB；
agent 四平台 7.3–8.0 MB。Linux 产物 `file` 报 `statically linked`。

### 2. 容器与登录

```
$ docker save orciny:dev | wc -c        →  13.2 MB（压缩）
$ docker run --rm --entrypoint sh orciny:dev -c 'du -sh /'  →  35.8M（未压缩）
```

> **注意一个测量陷阱：** `docker images` 对这个镜像报 **51.6MB**，超了 40MB 目标。
> 那个数字不可信——Docker Desktop 的 containerd 镜像存储把压缩 blob 与解包后的
> snapshot 两份都算进去了（13.2 + 35.8 ≈ 49，余下是 manifest）。`docker save`
> 与容器内 `du` 两个独立口径互相吻合，以它们为准。核对体积时不要只看
> `docker images`。

`GET /` 返回真实 UI（`<script src="/assets/index-*.js">`），不是
`hub/internal/site/dist/index.html` 那个占位页；JS/CSS 资源各返回 200。
建超级用户后 `machines` 集合 `totalItems = 0`，即空列表。

### 3. 一行接入（待真机验收）

已在 Debian trixie 容器（**真 systemd 257**，非 mock）里以非 root 目标用户
`alice` 走通完整链路：

```
==> 平台: linux/arm64
==> 运行身份: alice (/home/alice)
==> 下载 orciny-agent_0.1.0_linux_arm64.tar.gz
==> 校验和通过
==> 安装到 /usr/local/bin/orciny-agent
==> 注册到 hub
==> 写入 /etc/systemd/system/orciny-agent.service
==> 服务已启动
```

面板随即转 online，事件序列 `token.issued → machine.enrolled → machine.connected`。
服务进程 uid 1000（`alice`），**不是 root**；`~/.orciny` 及其内容全部 0600/0700
且属主正确。

顺带验过的守卫（逐条都明确中止，不留半装状态）：缺 `--hub` / 缺 `--token`、
未知参数、以 root 运行、sha256 不匹配。

**待补：** ≥1 台 macOS 真机（launchd 路径 + `plutil` 之外的实际加载）、共 3 台，
每台用 `time` 掐表确认 < 2 分钟。容器里的安装是秒级完成的，真机的时间主要花在
下载上。

### 4. kill -9

```
kill -9 前:  online
转 offline:  5.11 秒   （OfflineGrace = 5s）
手工重启后:  0.30 秒 转回 online
```

5.11 s 落在 DoD 的 5–10 s 区间内。

**待补：** 「服务自动重启」这半条走的是 systemd `Restart=always` /
launchd `KeepAlive`，本轮是手工重启进程代替的。单元文件里两个键都在
（`systemd-analyze verify` 通过、`plutil -lint` OK），但真机上要实测一次。

### 5. 断网 60 秒 —— 部分通过

用一个自写的 TCP 黑洞代理模拟**真实断网**（连接保持打开、双向都不再有数据，
两端都收不到 FIN/RST）——这比 `docker stop` 更接近真相：后者会立刻给出 RST，
测不到读超时那条路径。

hub 自己写的事件给出了权威时间线：

```
02:58:04.325  machine.connected
02:58:13 前后  断网开始
02:59:14.378  agent 日志: read tcp …: i/o timeout   （agent 侧 70s 读超时）
02:59:19.338  machine.disconnected                  （状态转 offline）
02:59:19.983  machine.connected                     （恢复后 0.31s 重连）
```

- **恢复自动重连：0.31 秒，无人工干预** ✅
- **转 offline：自断网起 66.3 秒**，自连接建立起 75.0 秒 ❌ 超出 DoD 的「60 秒」

**定位：** 这不是缺陷，是 DoD 的数字与 spec 自己的超时设置不自洽。
`ReadTimeout = 70s`（spec §6.2）+ `OfflineGrace = 5s`（§6.3）= 最坏 75 秒才可能
判定离线。静默断网下没有任何一方收到 TCP 错误，只能靠读超时兜底，因此
**60 秒这个数字在当前参数下不可能达到**。

**处置建议：** 改 DoD 的数字而不是改超时——把第 5 条写成「断网后 75 秒内转
offline」。70 秒的读超时本身是有理由的（要容得下 30 秒心跳漏两拍），为了凑
一个 60 秒的验收数字去压它反而会让抖动误判增多。记入 M1 待办（文档修正）。

### 6. 重启 hub

```
重启前:                          online
hub 重启完成那一刻库里的状态:      offline   ← ResetGhosts 已翻掉幽灵
1.46 秒后:                        online
```

无残留幽灵 online，这是 `OnServe` 里 `machines.ResetGhosts()` 在 WS 端点接客
之前就把库里的 online 翻掉的效果。

### 7. 篡改 hub.pub

```json
{"level":"ERROR","msg":"hub 签名验证失败，停止一切重试。请确认 hub 是否更换过密钥，或本机是否遭到中间人攻击","error":"conn: hub 签名验证失败"}
```

进程随即退出（`Error: conn: hub 签名验证失败`），`orciny-agent status` 显示
`连接状态 compromised`，此后 20 秒零重试。与 spec §7.2 的 `Compromised` 终态
完全一致。

> **计划 9 Task 6 Step 2 给的篡改命令是个无效测试，别照用。** 它写的是
> ```
> printf 'AAAAC3NzaC1lZDI1NTE5AAAA...\n' > ~/.orciny/identity/hub.pub
> ```
> 那是 **SSH 格式**的 ed25519 公钥 blob，base64 解出来 48 字节，而 `hub.pub`
> 存的是裸 32 字节公钥的 base64。于是 agent 在**加载**阶段就报
> 「公钥长度应为 32 字节，实际 48」，压根走不到签名验证，落进通用指数退避
> **并且会一直重试下去**——看起来像是第 7 条不通过，其实是测试用例错了。
>
> 正确的篡改方式是换成一枚**格式合法但不是 hub 的**公钥：
> ```bash
> python3 -c "import os,base64;print(base64.b64encode(os.urandom(32)).decode())" \
>   > ~/.orciny/identity/hub.pub
> ```
>
> 顺带暴露一个次要问题：**hub.pub 内容无法解析时，agent 走的是通用指数退避、
> 无限重试**，而不是 `Compromised`。一个损坏/被替换成垃圾的钉扎公钥同样是
> 「需要人来判断」的情形，按 spec §7.2 的精神也该进终态。记入 M1 待办。

### 8. UI 删除机器 —— 首测未通过，已修，复测通过

**首测（未通过）。** 删除记录后 agent 的日志：

```json
{"level":"WARN","msg":"与 hub 的连接已断开","error":"gws: connection closed, code=1000, reason=machine removed"}
{"level":"WARN","msg":"hub 拒绝连接，将按固定间隔重试","code":1,"reason":"该指纹未登记，请重新执行 orciny-agent enroll","interval":300000000000}
```

agent 确实拿到了「明确原因」，但**没有停止重试**：它落进了
`CodeUnknownFingerprint`（code=1）分支，转为每 5 分钟固定间隔无限重试。
进程也没退出。

**定位（已确认到代码行）：** hub 侧是对的——`hub/internal/machines/hooks.go:42`
在关连接之前确实发了 `AuthResult{OK:false, Code:CodeMachineRemoved}`。
问题在 agent 侧：`agent/internal/conn/dial.go` 的 `OnMessage` 把收到的帧塞进
`h.inbox`，而 `h.inbox` 的**唯一**消费者是同文件里的 `await`（dial.go:279），
它只在**握手期间**被调用。握手完成后没有任何代码再读 `inbox`，于是这条
「授权已撤销」通知进了带缓冲的 channel 就再没人看，agent 只能从随后的 WS
close 帧间接得知，重连时才撞上 code=1。

这正好是 spec §3.3 明确要求的那条路径落空了：

> `AuthResult` 有一个额外用途：**握手完成后 hub 仍可发送 `AuthResult{OK:false}`
> 作为「授权已撤销」通知**……agent 在任何时候收到 `OK:false` 都按 `Code` 走
> §7.2 的分流，不区分是握手期还是连接期——这样 agent 侧只有一条处理路径。

而实现里恰恰**只有握手期那一条路径**。

**影响：** 不是安全问题（hub 一直在拒），但被删机器的 agent 会永远每 5 分钟
敲一次门，且违反 spec §7.2 与 §6.4c。

**已修（提交 `1666650`）。** 两处改动：

1. `handler` 加 `connected` 标志，握手完成后的帧交给新的 `onConnected`：
   收到 `AuthResult{OK:false}` 即记为连接结束原因并关连接，`OK:true` 忽略。
   标志在发 `MachineInfo` **之前**置位，并补一次 `inbox` 排空——hub 发出
   `AuthResult{OK:true}` 之后就登记了连接，撤销通知随时可能到来，晚一步
   置位就又漏了。
2. `Client.Run` 在 session 结束时，若原因是 `RejectedError` 就过一遍
   `classify`，与握手期的拒绝共用同一条分流。

配了四个用例（两个 conn 层、两个 client 层），改动前全部失败、改动后全部
通过，`-race` 干净。

**复测（通过）：**

```json
{"level":"WARN","msg":"hub 撤销了本连接的授权","error":"conn: hub 拒绝连接（code=4）: 该机器已在面板中被删除，请重新 enroll"}
{"level":"ERROR","msg":"hub 报告本机已被删除，停止重试；如需重新接入请执行 orciny-agent enroll","reason":"该机器已在面板中被删除，请重新 enroll"}
```

进程随即退出，此后 15 秒零重试。

顺带修掉的一件事：`ConnectOptions` 原先没有 `Logger` 字段，连接层拿不到
JSON logger，于是「撤销了本连接的授权」这条会以 slog 默认的**文本**格式打
出来，跟 agent 其余的 JSON 日志混在一起（首测时肉眼可见）。补上字段后全部
日志行都是 JSON。

### 9. 测试

```
$ go clean -testcache && go test -tags=testing ./...
全部 ok，real 7.37s
顶层用例 203 个，含子测试共 213 个
```

`make lint`（`go vet` + `gofmt -l`）同样干净。

### 10. 资源占用

| | 实测 | 目标 |
|---|---|---|
| hub（容器，空闲）| **34.84 MiB** RAM，CPU 0.02% | < 64MB |
| agent（macOS 原生，已连接空闲）| **13.6 MB** RSS，CPU **0.0%** | < 30MB，CPU ≈ 0 |

agent 连续两次采样（间隔 10 秒）RSS 都是 13.6 MB、CPU 0.0%，没有增长趋势。

---

## 待办汇总

| 事项 | 类型 | 归属 |
|---|---|---|
| ~~连接期不消费 `inbox`，`CodeMachineRemoved` 撤销通知被丢弃（第 8 条）~~ | 代码缺陷 | **已修，提交 `1666650`** |
| `hub.pub` 无法解析时无限重试，而非进入 `Compromised` | 代码缺陷（次要）| M1 |
| DoD 第 5 条的「60 秒」与 spec 的 70s 读超时不自洽，应改为 75 秒 | 文档修正 | M1 |
| 计划 9 Task 6 Step 2 的篡改命令是无效测试（SSH 格式公钥）| 文档修正 | 已在本文件更正 |
| 3 台真机（≥1 macOS）接入计时、systemd/launchd 自动重启实测 | 待真机验收 | 有真机时补 |

---

## 复现方式

本轮用到的临时环境全部建在会话 scratchpad 里，验收结束已清理：宿主
`~/.orciny`、`/usr/local/bin/orciny-agent`、`~/Library/LaunchAgents/` 均未被
写入（agent 全程用 `ORCINY_HOME` 指向 scratchpad）。

要复现单机部分，大致是：

```bash
# hub
docker build -f supplemental/docker/Dockerfile -t orciny:dev --build-arg VERSION=0.1.0 .
docker run -d --name orciny-acc -p 8090:8090 orciny:dev
docker exec orciny-acc orciny superuser create you@example.com '够长的密码' --dir=/pb_data

# agent（注意版本必须是正式号：0.1.0-SNAPSHOT-xxx 按 semver 低于 0.1.0，
# 会被 MinAgentVersion 门槛以 code=3 拒掉）
go build -ldflags "-X github.com/FlintyLemming/orciny.Version=0.1.0" -o /tmp/orciny-agent ./cmd/orciny-agent
ORCINY_HOME=/tmp/ah /tmp/orciny-agent enroll --hub http://127.0.0.1:8090 --token <t> --hub-key <fp>
ORCINY_HOME=/tmp/ah /tmp/orciny-agent run
```

Linux + systemd 那一段用 `debian:trixie-slim` 装 `systemd systemd-sysv curl sudo`，
以 `--privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw
--tmpfs /run --tmpfs /run/lock` 起 `/sbin/init`，再建一个非 root 用户执行安装命令。
