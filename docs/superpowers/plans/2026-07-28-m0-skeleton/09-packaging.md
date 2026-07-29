# M0 计划 9 · 打包与分发 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让别人（包括三个月后的自己）能把 Orciny 装起来：hub 的容器与 compose 示例、两个二进制的 release 产物、一行接入的 `install.sh`、systemd / launchd 服务单元、运维文档，最后跑完 spec §13 的十条验收。

**Architecture:** goreleaser 出 `CGO_ENABLED=0` 的静态二进制并附 `checksums.txt`；`install.sh` 由 hub 自己托管（`GET /install.sh`，模板注入 hub URL），脚本探测平台 → 下载 → 校验 sha256 → 装服务单元 → 调 `enroll` → 起服务。服务单元**以目标用户身份运行**——这是与 beszel 的关键差异，M1 起 agent 要读写 `~/.claude`，属主必须正确。

**Tech Stack:** Docker 多阶段构建（Node → Go → alpine）· goreleaser · POSIX sh · systemd · launchd

**上位文档：** [统括计划](00-overview.md) · [spec §11 §13 §4.5](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`；依赖版本锁死（见统括计划）。
- 二进制名 `orciny` / `orciny-agent`；agent 目录 `~/.orciny/`；镜像名 `orciny`。
- 体积目标（产品文档 §9）：单二进制各 < 30MB，docker 镜像 < 40MB。
- 平台矩阵：hub = `linux/amd64` `linux/arm64`；agent = `linux/amd64` `linux/arm64` `darwin/amd64` `darwin/arm64`。
- `install.sh` 只用 POSIX sh，不依赖 bash 特性；不打印完整 token（只打印前 8 位）。
- agent 服务单元必须以**目标用户**身份运行，不是专用系统用户。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `supplemental/docker/Dockerfile` | 多阶段构建 hub 镜像 |
| `supplemental/docker/docker-compose.yml` | 部署示例（含反向代理说明） |
| `.dockerignore` | 削掉构建上下文 |
| `.goreleaser.yml` | 两个二进制 × 平台矩阵 + checksums |
| `hub/internal/routes/install-agent.sh` | 一行接入脚本的**唯一副本**（`//go:embed` 只能引用同目录及子目录的文件） |
| `supplemental/scripts/install-agent.sh` | 指向上面那份的符号链接，保持 spec §2.1 的目录布局 |
| `hub/internal/routes/install.go` | `GET /install.sh`，模板注入版本与下载源 |
| `hub/internal/routes/install_test.go` | 路由测试 |
| `supplemental/units/orciny-agent.service` | systemd system unit 模板 |
| `supplemental/units/moe.flinty.orciny-agent.plist` | launchd LaunchAgent 模板 |
| `docs/operations.md` | 运维文档：备份与恢复、密钥丢失、macOS 限制 |
| `README.md` | 修改：安装与快速上手 |

---

## Task 1: hub 容器与 compose 示例

**Files:**
- Create: `supplemental/docker/Dockerfile`, `supplemental/docker/docker-compose.yml`, `.dockerignore`

**Interfaces:**
- Consumes: `Makefile` 的构建目标、`hub/internal/site` 的 npm 工程
- Produces: 镜像 `orciny:dev`，暴露 8090，数据卷 `/pb_data`

- [ ] **Step 1: 写 .dockerignore**

创建仓库根的 `.dockerignore`：

```gitignore
.git
dist
pb_data
**/node_modules
docs
mock
*.md
```

- [ ] **Step 2: 写 Dockerfile**

创建 `supplemental/docker/Dockerfile`：

```dockerfile
# 三段：Node 编前端 → Go 编二进制（把 dist 一起 embed）→ alpine 运行时。
# 前端产物必须在 go build 之前就位，否则 //go:embed all:dist 拿到的是占位页。

FROM node:24-alpine AS web
WORKDIR /src/hub/internal/site
COPY hub/internal/site/package.json hub/internal/site/package-lock.json ./
RUN npm ci
COPY hub/internal/site/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/hub/internal/site/dist ./hub/internal/site/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/FlintyLemming/orciny.Version=${VERSION}" \
      -o /out/orciny ./cmd/orciny

FROM alpine:3.22
# ca-certificates：hub 要以 HTTPS 访问外部（M2 的 fetch 型采集器）
# tzdata：日志与事件时间戳按部署地时区显示
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/orciny /usr/local/bin/orciny
VOLUME /pb_data
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/orciny"]
CMD ["serve", "--http=0.0.0.0:8090", "--dir=/pb_data"]
```

- [ ] **Step 3: 写 compose 示例**

创建 `supplemental/docker/docker-compose.yml`：

```yaml
# Orciny hub 的最小部署示例。
#
# TLS 不在这里做：hub 假定跑在反向代理之后（Caddy / Nginx / Traefik）。
# 这一点不是省事，是必须——agent 的信任根建立在 enroll 那一刻的 HTTPS 上
# （spec §4.4），明文 HTTP 暴露到公网等于把首次接入交给中间人。
services:
  orciny:
    image: ghcr.io/flintylemming/orciny:latest
    container_name: orciny
    restart: unless-stopped
    ports:
      # 只监听回环，由宿主上的反向代理转发。
      # 若在内网直连使用，改成 "8090:8090"。
      - "127.0.0.1:8090:8090"
    volumes:
      # 整个应用的状态就是这一个目录：SQLite + 上传文件 + hub 私钥。
      # 备份它就等于备份了 Orciny。
      - ./pb_data:/pb_data
    environment:
      - TZ=Asia/Shanghai

# Caddy 示例（另存为 Caddyfile 并按需启用）：
#
#   orciny.example.com {
#     reverse_proxy 127.0.0.1:8090
#   }
#
# WebSocket 无需额外配置——Caddy 默认透传 Upgrade。
# 若用 Nginx，务必加：
#   proxy_http_version 1.1;
#   proxy_set_header Upgrade $http_upgrade;
#   proxy_set_header Connection "upgrade";
#   proxy_read_timeout 300s;   # 大于 hub 的 70s read deadline
```

- [ ] **Step 4: 验收**

```bash
docker build -f supplemental/docker/Dockerfile -t orciny:dev --build-arg VERSION=0.1.0 .
docker images orciny:dev --format '{{.Size}}'      # 期望 < 40MB
docker run --rm -p 8090:8090 -v /tmp/orciny-pb:/pb_data orciny:dev
```

浏览器访问 `http://127.0.0.1:8090`，确认前端是真实 UI 而非占位页。
再确认 `docker run --rm orciny:dev --version` 之类的启动路径不报错（PocketBase 的根命令带 `--help`）。

清理：`docker rm -f $(docker ps -aq --filter ancestor=orciny:dev) 2>/dev/null; sudo rm -rf /tmp/orciny-pb`

- [ ] **Step 5: 提交**

```bash
git add supplemental/docker .dockerignore
git commit -m "build: hub 容器镜像与 compose 部署示例"
```

---

## Task 2: goreleaser 产物

**Files:**
- Create: `.goreleaser.yml`

**Interfaces:**
- Produces: `dist/` 下的两个二进制 × 平台矩阵、`checksums.txt`（`install.sh` 依赖它的文件名格式）

- [ ] **Step 1: 写配置**

创建 `.goreleaser.yml`：

```yaml
version: 2

before:
  hooks:
    # 前端必须先构建：go build 会 embed dist/
    - sh -c "cd hub/internal/site && npm ci && npm run build"

builds:
  - id: hub
    main: ./cmd/orciny
    binary: orciny
    env: [CGO_ENABLED=0]
    flags: [-trimpath]
    ldflags:
      - -s -w -X github.com/FlintyLemming/orciny.Version={{.Version}}
    goos: [linux]
    goarch: [amd64, arm64]

  - id: agent
    main: ./cmd/orciny-agent
    binary: orciny-agent
    env: [CGO_ENABLED=0]
    flags: [-trimpath]
    ldflags:
      - -s -w -X github.com/FlintyLemming/orciny.Version={{.Version}}
    goos: [linux, darwin]
    goarch: [amd64, arm64]

archives:
  # 归档名直接决定 install.sh 的下载 URL，改动这里必须同步改脚本。
  - id: hub
    ids: [hub]
    name_template: "orciny_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]
  - id: agent
    ids: [agent]
    name_template: "orciny-agent_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]

checksum:
  name_template: checksums.txt

changelog:
  use: github
  sort: asc
  filters:
    exclude: ["^docs:", "^test:", "^chore:"]

release:
  draft: true
  prerelease: auto
```

- [ ] **Step 2: 验收**

```bash
goreleaser build --snapshot --clean
ls -lh dist/*/orciny dist/*/orciny-agent
```

逐条确认：

- 六个产物齐全（hub 两个平台、agent 四个平台）。
- 每个二进制 < 30MB。
- `file dist/hub_linux_amd64_v1/orciny` 显示 statically linked。
- 本机架构对应的 agent 能跑：`./dist/agent_darwin_arm64/orciny-agent version` 输出版本号（在 macOS 上；Linux 换对应目录）。

```bash
goreleaser release --snapshot --clean --skip=publish
ls dist/*.tar.gz dist/checksums.txt
```

确认归档名形如 `orciny-agent_0.1.0-SNAPSHOT_linux_arm64.tar.gz`，`checksums.txt` 里每行是 `<sha256>  <文件名>`。**记下这个命名格式，Task 3 的脚本要按它拼 URL。**

- [ ] **Step 3: 提交**

```bash
git add .goreleaser.yml
git commit -m "build: goreleaser 产物矩阵与校验和"
```

---

## Task 3: 服务单元

**Files:**
- Create: `supplemental/units/orciny-agent.service`, `supplemental/units/moe.flinty.orciny-agent.plist`

**Interfaces:**
- Produces: 两个含占位符的模板，`install.sh` 用 `sed` 替换 `__USER__` / `__HOME__` / `__BIN__`

- [ ] **Step 1: 写 systemd unit**

创建 `supplemental/units/orciny-agent.service`：

```ini
# Orciny agent —— systemd system unit
#
# 关键差异（spec §11.3）：agent 不跑在专用系统用户下，而是以**目标用户**
# 身份运行。从 M1 起 agent 要读写 ~/.claude，属主必须正确，否则用户自己
# 反而动不了那些文件。
#
# 选 system unit 而非 user unit：user unit 需要 loginctl enable-linger
# 才能在用户未登录时运行，多一个容易漏掉的步骤。
#
# 占位符由 install.sh 替换：__USER__ __HOME__ __BIN__
[Unit]
Description=Orciny agent
Documentation=https://github.com/FlintyLemming/orciny
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=__USER__
Group=__USER__
Environment=HOME=__HOME__
ExecStart=__BIN__ run
Restart=always
RestartSec=5s

# agent 只需要读写自己的 ~/.orciny 与（M1 起）~/.claude。
# 这里不加 ProtectHome —— 那恰好会挡住它要干的正事。
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full

# 空闲时 CPU 应当接近 0；给一个宽松的内存上限兜底（目标 < 30MB）。
MemoryMax=128M

StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 2: 写 launchd plist**

创建 `supplemental/units/moe.flinty.orciny-agent.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!--
  Orciny agent —— macOS LaunchAgent

  装在 ~/Library/LaunchAgents/，随用户登录启动，天然以该用户身份运行——
  正是 M1 读写 ~/.claude 需要的属主（spec §11.3）。

  已知限制：headless 的 Mac mini 在无人登录时 LaunchAgent 不运行。
  这是 macOS 的固有特性，M0 不解决；两条出路见 docs/operations.md。

  占位符由 install.sh 替换：__BIN__ __HOME__
-->
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>moe.flinty.orciny-agent</string>

  <key>ProgramArguments</key>
  <array>
    <string>__BIN__</string>
    <string>run</string>
  </array>

  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>__HOME__</string>
    <!-- claude 通常装在这些位置；LaunchAgent 的 PATH 很窄，要显式给全 -->
    <key>PATH</key>
    <string>__HOME__/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>

  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>

  <key>StandardOutPath</key>
  <string>__HOME__/.orciny/logs/launchd.out.log</string>
  <key>StandardErrorPath</key>
  <string>__HOME__/.orciny/logs/launchd.err.log</string>
</dict>
</plist>
```

- [ ] **Step 3: 验收**

在一台 Linux 机器（或容器）上手工验证模板可用：

```bash
sed -e "s|__USER__|$USER|g" -e "s|__HOME__|$HOME|g" \
    -e "s|__BIN__|/usr/local/bin/orciny-agent|g" \
    supplemental/units/orciny-agent.service > /tmp/orciny-agent.service
systemd-analyze verify /tmp/orciny-agent.service   # 期望：无输出
```

在 macOS 上：

```bash
sed -e "s|__BIN__|/usr/local/bin/orciny-agent|g" -e "s|__HOME__|$HOME|g" \
    supplemental/units/moe.flinty.orciny-agent.plist > /tmp/t.plist
plutil -lint /tmp/t.plist    # 期望：OK
```

- [ ] **Step 4: 提交**

```bash
git add supplemental/units
git commit -m "build: systemd 与 launchd 服务单元模板"
```

---

## Task 4: install.sh 与托管路由

**Files:**
- Create: `hub/internal/routes/install-agent.sh`（+ `supplemental/scripts/install-agent.sh` 符号链接）, `hub/internal/routes/install.go`, `hub/internal/routes/install_test.go`
- Modify: `hub/internal/routes/routes.go`（注册 `GET /install.sh`）, `hub/hub.go`（把脚本内容传进 `routes.Deps`）

**Interfaces:**
- Produces:
  ```go
  // hub/internal/routes
  type Deps struct {
      // …既有字段…
      DownloadBase string // 默认 GitHub releases；开发期可覆盖
  }
  const DefaultDownloadBase = "https://github.com/FlintyLemming/orciny/releases/latest/download"
  func InstallScript(version, downloadBase string) string
  ```
  脚本源文本由 `//go:embed` 内嵌在 routes 包里，不经 `Deps` 传递。
  脚本参数：`--hub <url>` `--token <t>` `--hub-key <fp>` `--version <v>` `--download-base <url>`

- [ ] **Step 1: 写脚本**

创建 `hub/internal/routes/install-agent.sh`（脚本要被 `//go:embed` 内嵌，
必须放在 routes 包目录里；随后建一个符号链接维持 spec §2.1 的目录布局：
`ln -s ../../hub/internal/routes/install-agent.sh supplemental/scripts/install-agent.sh`）：

```sh
#!/bin/sh
# Orciny agent 安装脚本
#
# 参照 beszel 的 install-agent.sh，但砍掉长尾（Alpine / OpenWrt / FreeBSD /
# SELinux 等留给 M3）。只用 POSIX sh，不依赖 bash 特性。
#
# 用法：
#   curl -fsSL https://<hub>/install.sh | sh -s -- --hub <url> --token <t> [--hub-key <fp>]
set -eu

HUB=""
TOKEN=""
HUB_KEY=""
VERSION="__VERSION__"
DOWNLOAD_BASE="__DOWNLOAD_BASE__"
BIN_PATH="/usr/local/bin/orciny-agent"

die() { echo "错误: $*" >&2; exit 1; }
info() { echo "==> $*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --hub) HUB="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --hub-key) HUB_KEY="${2:-}"; shift 2 ;;
    --version) VERSION="${2:-}"; shift 2 ;;
    --download-base) DOWNLOAD_BASE="${2:-}"; shift 2 ;;
    -h|--help)
      echo "用法: $0 --hub <url> --token <t> [--hub-key <fp>] [--version <v>] [--download-base <url>]"
      exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

[ -n "$HUB" ] || die "缺少 --hub"
[ -n "$TOKEN" ] || die "缺少 --token"

# token 只回显前 8 位（spec §7.5）
info "hub: $HUB   token: $(printf '%s' "$TOKEN" | cut -c1-8)…"

# ---- 1. 探测平台 -------------------------------------------------------
OS="$(uname -s)"
case "$OS" in
  Linux) GOOS="linux" ;;
  Darwin) GOOS="darwin" ;;
  *) die "M0 只支持 Linux 与 macOS，当前是 $OS" ;;
esac

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) die "不支持的架构: $ARCH" ;;
esac
info "平台: ${GOOS}/${GOARCH}"

# ---- 2. 确定运行身份 ---------------------------------------------------
# agent 必须以目标用户身份运行（M1 起要读写 ~/.claude，属主必须正确）。
# 用 sudo 跑脚本时，真正的用户是 SUDO_USER 而不是 root。
RUN_USER="${SUDO_USER:-$(id -un)}"
[ "$RUN_USER" != "root" ] || die "不要以 root 作为 agent 的运行身份；请用目标用户执行（可带 sudo）"

if [ "$GOOS" = "darwin" ]; then
  RUN_HOME="$(eval echo "~$RUN_USER")"
else
  RUN_HOME="$(getent passwd "$RUN_USER" | cut -d: -f6)"
fi
[ -n "$RUN_HOME" ] || die "找不到用户 $RUN_USER 的家目录"
info "运行身份: $RUN_USER ($RUN_HOME)"

# ---- 3. 下载并校验 -----------------------------------------------------
SUDO=""
[ "$(id -u)" -eq 0 ] || SUDO="sudo"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ARCHIVE="orciny-agent_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
info "下载 $ARCHIVE"
curl -fsSL -o "$TMP/$ARCHIVE" "$DOWNLOAD_BASE/$ARCHIVE" \
  || die "下载失败: $DOWNLOAD_BASE/$ARCHIVE"

if curl -fsSL -o "$TMP/checksums.txt" "$DOWNLOAD_BASE/checksums.txt" 2>/dev/null; then
  EXPECTED="$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | awk '{print $1}')"
  [ -n "$EXPECTED" ] || die "checksums.txt 里没有 $ARCHIVE 的记录"
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')"
  else
    ACTUAL="$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')"
  fi
  [ "$EXPECTED" = "$ACTUAL" ] || die "校验和不匹配，已中止安装"
  info "校验和通过"
else
  # 开发期指向本地构建产物时可能没有 checksums.txt，明确告知而不是静默跳过。
  echo "警告: 没有找到 checksums.txt，跳过校验" >&2
fi

tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
[ -f "$TMP/orciny-agent" ] || die "归档里没有 orciny-agent"

# ---- 4. 安装二进制 -----------------------------------------------------
info "安装到 $BIN_PATH"
$SUDO install -m 0755 "$TMP/orciny-agent" "$BIN_PATH"

# ---- 5. enroll ---------------------------------------------------------
# 以目标用户身份执行，密钥与配置才会落在正确的家目录且属主正确。
info "注册到 hub"
ENROLL_ARGS="enroll --hub $HUB --token $TOKEN"
[ -n "$HUB_KEY" ] && ENROLL_ARGS="$ENROLL_ARGS --hub-key $HUB_KEY"

if [ "$(id -un)" = "$RUN_USER" ]; then
  # shellcheck disable=SC2086
  "$BIN_PATH" $ENROLL_ARGS
else
  # shellcheck disable=SC2086
  $SUDO -u "$RUN_USER" HOME="$RUN_HOME" "$BIN_PATH" $ENROLL_ARGS
fi

# ---- 6. 装服务单元 -----------------------------------------------------
if [ "$GOOS" = "linux" ]; then
  UNIT=/etc/systemd/system/orciny-agent.service
  info "写入 $UNIT"
  $SUDO sh -c "cat > $UNIT" <<UNITEOF
[Unit]
Description=Orciny agent
Documentation=https://github.com/FlintyLemming/orciny
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$RUN_USER
Group=$RUN_USER
Environment=HOME=$RUN_HOME
ExecStart=$BIN_PATH run
Restart=always
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
MemoryMax=128M
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNITEOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable --now orciny-agent
  sleep 2
  $SUDO systemctl is-active --quiet orciny-agent \
    || die "服务未能启动，请查看: journalctl -u orciny-agent -n 50"
  info "服务已启动: systemctl status orciny-agent"
else
  PLIST="$RUN_HOME/Library/LaunchAgents/moe.flinty.orciny-agent.plist"
  info "写入 $PLIST"
  mkdir -p "$RUN_HOME/Library/LaunchAgents" "$RUN_HOME/.orciny/logs"
  cat > "$PLIST" <<PLISTEOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>moe.flinty.orciny-agent</string>
  <key>ProgramArguments</key>
  <array><string>$BIN_PATH</string><string>run</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key><string>$RUN_HOME</string>
    <key>PATH</key><string>$RUN_HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$RUN_HOME/.orciny/logs/launchd.out.log</string>
  <key>StandardErrorPath</key><string>$RUN_HOME/.orciny/logs/launchd.err.log</string>
</dict>
</plist>
PLISTEOF
  launchctl unload "$PLIST" 2>/dev/null || true
  launchctl load "$PLIST"
  sleep 2
  launchctl list | grep -q moe.flinty.orciny-agent \
    || die "服务未能启动，请查看 $RUN_HOME/.orciny/logs/launchd.err.log"
  info "服务已启动: launchctl list | grep orciny"
fi

info "完成。机器应当在几秒内出现在面板上。"
```

```bash
chmod +x hub/internal/routes/install-agent.sh
mkdir -p supplemental/scripts
ln -s ../../hub/internal/routes/install-agent.sh supplemental/scripts/install-agent.sh
```

- [ ] **Step 2: 写托管路由的失败测试**

创建 `hub/internal/routes/install_test.go`：

```go
package routes_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallScriptIsPubliclyServed(t *testing.T) {
	f := newFixture(t)

	// 脚本本身不含秘密，token 由用户拼在命令行上，因此不需要认证（spec §9.1）。
	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "#!/bin/sh")
	require.Contains(t, string(body), "orciny-agent")
}

func TestInstallScriptHasPlaceholdersFilled(t *testing.T) {
	f := newFixture(t)

	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	s := string(body)
	require.NotContains(t, s, "__VERSION__", "版本占位符必须已注入")
	require.NotContains(t, s, "__DOWNLOAD_BASE__", "下载地址占位符必须已注入")
	require.Contains(t, s, "0.1.0")
}

func TestInstallScriptServedAsShellText(t *testing.T) {
	f := newFixture(t)

	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Contains(t, resp.Header.Get("Content-Type"), "text/plain",
		"当成 HTML 送会被浏览器渲染，也可能被中间设备改写")
}
```

这三条用例复用计划 4 Task 4 建的 `newFixture`（同一个 `routes_test` 包）。
`newFixture` 里的 `routes.Deps` 无需改动——`DownloadBase` 留空即走
`routes.DefaultDownloadBase`，`Version` 已经是 `"0.1.0"`。

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./hub/internal/routes/... -run Install -v`
Expected: FAIL —— 404

- [ ] **Step 4: 写路由**

创建 `hub/internal/routes/install.go`：

```go
package routes

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// installScript 是内嵌的安装脚本源文本。
//
// 内嵌而不是运行时读文件：hub 是单二进制部署，多一个必须随行的文件
// 就是多一种「装完发现少了东西」的故障。
//
//go:embed install-agent.sh
var installScript string

// DefaultDownloadBase 是 release 产物的默认位置。
// 开发期用 --download-base 指向本地构建产物，避免每次都要发 release
// 才能测试安装路径（spec §11.4）。
const DefaultDownloadBase = "https://github.com/FlintyLemming/orciny/releases/latest/download"

// InstallScript 返回注入了版本与下载地址的脚本文本。
func InstallScript(version, downloadBase string) string {
	if downloadBase == "" {
		downloadBase = DefaultDownloadBase
	}
	r := strings.NewReplacer(
		"__VERSION__", version,
		"__DOWNLOAD_BASE__", downloadBase,
	)
	return r.Replace(installScript)
}

func (d Deps) installSh(e *core.RequestEvent) error {
	// text/plain 而不是 text/x-shellscript：前者到处都能正确透传，
	// 后者在某些代理上会触发下载对话框。
	return e.String(http.StatusOK, InstallScript(d.Version, d.DownloadBase))
}
```

在 `routes.go` 的 `Deps` 上加字段并注册路由：

```go
type Deps struct {
	Enroll       *enroll.Service
	Identity     *identity.Store
	WS           *ws.Handler
	Version      string
	DownloadBase string
}

// Register 中，在 g 之外（install.sh 不在 /api 下）：
	e.Router.GET("/install.sh", d.installSh)
```

`hub/hub.go` 里给 `routes.Deps` 传 `DownloadBase: h.cfg.DownloadBase`，并在 `hub.Config` 加这个字段（零值走 `routes.DefaultDownloadBase`）。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test -tags=testing ./hub/... -v`
Expected: PASS

- [ ] **Step 6: 静态检查脚本**

```bash
shellcheck hub/internal/routes/install-agent.sh
sh -n hub/internal/routes/install-agent.sh    # 语法检查
```

shellcheck 若报 SC2086（未加引号的变量展开）——脚本里已用 `# shellcheck disable` 标注了两处**有意**的词分割；其余告警都要修掉。

- [ ] **Step 7: 端到端演练（开发期下载源）**

```bash
# 1. 本地构建产物并起一个静态文件服务当作 download base
goreleaser release --snapshot --clean --skip=publish
(cd dist && python3 -m http.server 9000 &)

# 2. 起 hub，签发 token（步骤同计划 4 的手工验证）
make build && ./dist/orciny serve --http=127.0.0.1:8090 --dir=/tmp/orciny-demo &

# 3. 用签发出来的命令接入，但把下载源指到本地
curl -fsSL http://127.0.0.1:8090/install.sh | sh -s -- \
  --hub http://127.0.0.1:8090 --token <t> --hub-key <fp> \
  --download-base http://127.0.0.1:9000 --version 0.1.0-SNAPSHOT
```

确认：二进制装到 `/usr/local/bin/orciny-agent`、服务已 enable 并 active、面板上出现该机器且状态在线。

清理：`sudo systemctl disable --now orciny-agent; sudo rm -f /etc/systemd/system/orciny-agent.service /usr/local/bin/orciny-agent; rm -rf ~/.orciny /tmp/orciny-demo`（macOS 上换成 `launchctl unload` + 删 plist）

- [ ] **Step 8: 提交**

```bash
git add hub supplemental
git commit -m "feat: install.sh 与 hub 托管的安装脚本路由"
```

---

## Task 5: 运维文档与 README

**Files:**
- Create: `docs/operations.md`
- Modify: `README.md`

**Interfaces:** 无代码接口

- [ ] **Step 1: 写运维文档**

创建 `docs/operations.md`：

````markdown
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
````

- [ ] **Step 2: 更新 README**

在 `README.md` 现有内容之后插入这一节（细节都在运维手册里，这里只求最短路径）：

````markdown
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
````

同时把 README 顶部的一句话定位与「当前进度：M0」标注补上，指向
[产品设计文档](docs/PRODUCT-DESIGN.md)。

- [ ] **Step 3: 提交**

```bash
git add docs/operations.md README.md
git commit -m "docs: 运维手册与快速开始"
```

---

## Task 6: M0 验收

不写代码，但必须真的做完并把结果记下来。

**Files:**
- Create: `docs/superpowers/plans/2026-07-28-m0-skeleton/acceptance.md`（验收记录）

- [ ] **Step 1: 准备环境**

一台跑 hub 的机器（VPS 或内网服务器，走 HTTPS）+ 三台真机，至少一台 Linux、一台 macOS。

- [ ] **Step 2: 逐条验收 spec §13**

建 `acceptance.md`，按下表逐条记录「通过/不通过 + 实测数据」：

| # | 标准 | 怎么验 |
|---|---|---|
| 1 | `make build` 产出两个二进制，各 < 30MB | `ls -lh dist/orciny dist/orciny-agent`，记下实际体积 |
| 2 | `docker compose up -d` 起 hub，浏览器可登录并看到空机器列表 | 记下镜像体积 |
| 3 | 3 台真机各自一行接入，单台耗时 < 2 分钟 | 用 `time` 掐表，记三台各自耗时与平台 |
| 4 | `kill -9` agent → 面板 5–10 秒内转 offline；服务自动重启 → 转回 online | 记实测秒数 |
| 5 | 断网 60 秒 → 转 offline；恢复 → 自动重连转 online，无需人工干预 | 记恢复耗时 |
| 6 | 重启 hub → 三台机器全部自动重连，无残留幽灵 online | 记全部回到 online 的耗时 |
| 7 | 篡改某台 agent 的 `hub.pub` → 拒绝连接、日志明确报出、不再重试 | 贴日志原文 |
| 8 | 在 UI 删除一台机器 → 该 agent 收到明确原因并停止重试 | 贴日志原文 |
| 9 | `go test -tags=testing ./...` 全绿 | 贴用例总数与耗时 |
| 10 | hub < 64MB RAM，agent < 30MB RAM，agent 空闲 CPU ≈ 0 | `docker stats` / `ps -o rss,pcpu`，记实测值 |

第 7 条的具体做法：

```bash
# 在某台 agent 上
sudo systemctl stop orciny-agent
printf 'AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n' > ~/.orciny/identity/hub.pub
sudo systemctl start orciny-agent
journalctl -u orciny-agent -f
```

期望日志里出现「hub 签名验证失败，停止一切重试」，且此后不再有重连尝试。
验证完把机器重新 enroll 一次。

- [ ] **Step 3: 记录偏差**

任何一条不通过，在 `acceptance.md` 里写清现象与初步定位，并决定是「M0 内修」还是「记入 M1 待办」。不要为了让表格全绿而降低标准。

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/plans/2026-07-28-m0-skeleton/acceptance.md
git commit -m "docs: M0 验收记录"
```

---

## 完成检查

- [ ] `docker build` 出的镜像 < 40MB，起得来、前端是真实 UI
- [ ] `goreleaser build --snapshot` 产出六个二进制，各 < 30MB，静态链接
- [ ] `shellcheck` 与 `sh -n` 对 `install-agent.sh` 无告警
- [ ] `GET /install.sh` 无需认证、占位符已注入、Content-Type 是 text/plain
- [ ] systemd unit 通过 `systemd-analyze verify`，plist 通过 `plutil -lint`
- [ ] 服务单元以**目标用户**身份运行（`ps -o user= -C orciny-agent` 不是 root）
- [ ] `docs/operations.md` 写明了 hub 私钥丢失的后果与 macOS headless 限制
- [ ] `acceptance.md` 十条逐条有结论与实测数据
