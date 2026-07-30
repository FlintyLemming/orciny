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
  # enable 只登记开机自启，起进程要靠 restart。不能用 `enable --now`：
  # 服务已经 active 时它什么都不做，于是重装/升级后跑的还是旧二进制
  # （表现为 `orciny-agent status` 里挂着上个版本的陈旧错误）。
  # restart 对「未启动」与「已在跑」两种情形都对。
  $SUDO systemctl enable orciny-agent
  $SUDO systemctl restart orciny-agent
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
