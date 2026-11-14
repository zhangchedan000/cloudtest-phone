#!/usr/bin/env bash
# 安装 / 更新管家程序，做成开机自启的 systemd 服务。可重复执行。
#
# 用法（root）：
#   bash host/install-agent.sh                 # 在仓库目录里：拉最新代码 → 编译管理页和管家程序 → 安装 → 重启服务
#   bash host/install-agent.sh --binary 文件   # 直接装一个编译好的程序（例如 GitHub 自动编译下载的那个）
#   bash host/install-agent.sh --no-pull       # 不拉代码，用当前目录的代码编译
#
# 服务参数放在 /etc/prism-phone/agent.env（第一次安装时生成，之后不覆盖）：
#   AGENT_ARGS   管家程序的参数（开发机默认带 -dev-host；开了网络模块再加 -mihomo-api 127.0.0.1:9090）
#   PP_TOKEN     管理页口令；只监听本机（127.0.0.1）时可以不设，外网访问必须设
#
# 不动网络、不动 SSH。旧程序保留为 .prev，新程序起不来会自动换回去。
set -euo pipefail

log() { printf '[agent] %s\n' "$*"; }
die() { printf '[agent] 错误：%s\n' "$*" >&2; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
# systemd 定时任务里没有 HOME，Go 和 npm 找不到缓存目录（踩坑 #17）
export HOME=${HOME:-/root}
export GOPATH=${GOPATH:-$HOME/go}
export GOCACHE=${GOCACHE:-$HOME/.cache/go-build}
export PATH=$PATH:/usr/local/go/bin

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=/usr/local/bin/prism-phone-agent
ENV=/etc/prism-phone/agent.env
UNIT=/etc/systemd/system/prism-phone-agent.service
binary=""
pull=1
while [ $# -gt 0 ]; do
  case "$1" in
    --binary) binary=${2:?}; shift 2 ;;
    --no-pull) pull=0; shift ;;
    *) die "不认识的参数 $1" ;;
  esac
done

if [ -z "$binary" ]; then
  cd "$ROOT"
  if [ "$pull" = 1 ] && [ -d .git ]; then
    log "拉取最新代码"
    git pull --ff-only || die "git pull 失败（服务器有 GitHub 读权限吗？或者本地有改动）"
  fi
  command -v go >/dev/null || die "没有 Go（先运行 host/setup-host.sh）"
  command -v npm >/dev/null || die "没有 Node.js / npm"
  log "编译管理页"
  (cd web && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null)
  log "编译管家程序"
  binary=$(mktemp /tmp/prism-phone-agent.XXXXXX)
  bash agent/build.sh "$binary"
fi
[ -x "$binary" ] || chmod +x "$binary"
"$binary" -version >/dev/null || die "$binary 不是能运行的管家程序（架构不对？）"

mkdir -p /etc/prism-phone /opt/prism-phone/data
if [ ! -f "$ENV" ]; then
  cat > "$ENV" <<EOF
# 管家程序参数（改完执行 systemctl restart prism-phone-agent）
AGENT_ARGS=-listen 127.0.0.1:8099 -data /opt/prism-phone/data -host-name $(hostname) -dev-host
# PP_TOKEN=换成一串随机字符
EOF
  chmod 600 "$ENV"
  log "生成了 $ENV（开发机默认带 -dev-host；正式机器去掉它）"
fi

cat > "$UNIT" <<'EOF'
[Unit]
Description=prism-phone 管家程序（云手机管理）
After=network-online.target docker.service
Wants=docker.service

[Service]
EnvironmentFile=/etc/prism-phone/agent.env
ExecStart=/usr/local/bin/prism-phone-agent $AGENT_ARGS
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload

[ -f "$BIN" ] && cp -f "$BIN" "$BIN.prev"
install -m 0755 "$binary" "$BIN"
systemctl enable prism-phone-agent >/dev/null 2>&1
systemctl restart prism-phone-agent
sleep 3
if ! systemctl is-active --quiet prism-phone-agent; then
  log "新程序没起来，日志："
  journalctl -u prism-phone-agent -n 20 --no-pager || true
  if [ -f "$BIN.prev" ]; then
    cp -f "$BIN.prev" "$BIN" && systemctl restart prism-phone-agent
    die "已换回旧程序"
  fi
  die "安装失败"
fi
log "已安装并运行：$("$BIN" -version)。管理页：在自己电脑上 ssh -L 8099:127.0.0.1:8099 这台机器，然后打开 http://127.0.0.1:8099"
