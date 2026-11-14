#!/usr/bin/env bash
# 服务器跟 GitHub 自动同步：GitHub 上的 main 为准，有新提交就拉下来、编译、更新管家程序。
#
# 用法（root，在仓库目录里）：
#   bash host/auto-update.sh install   # 装成 systemd 定时任务，每 10 分钟检查一次
#   bash host/auto-update.sh run       # 立刻检查一次（定时任务调用的就是这个）
#   bash host/auto-update.sh status    # 看上次同步结果
#   bash host/auto-update.sh remove    # 取消定时任务
#
# 规矩：
#   - 服务器上的仓库目录不手改。万一有人改了，同步前先 git stash 存起来（不丢），再对齐 GitHub。
#   - 只更新管家程序（install-agent.sh 失败会自动换回旧程序）；不动网络规则、不动 SSH、不关正在运行的云手机。
#   - 日志：/opt/prism-phone/logs/auto-update.log
set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
LOG=/opt/prism-phone/logs/auto-update.log
BRANCH=${PP_BRANCH:-main}
log() { printf '%s %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG"; }
[ "$(id -u)" = 0 ] || { echo "请用 root 运行" >&2; exit 1; }
mkdir -p "$(dirname "$LOG")"

run() {
  cd "$REPO"
  exec 9>/run/prism-phone-auto-update.lock
  flock -n 9 || { log "上一次同步还没做完，跳过"; return 0; }
  git fetch -q origin "$BRANCH" || { log "连不上 GitHub，下次再试"; return 0; }
  local here there
  here=$(git rev-parse HEAD)
  there=$(git rev-parse "origin/$BRANCH")
  # 正在运行的管家程序是哪个提交编译的（-version 输出 git describe，例如 6809783 或 6809783-dirty）
  local running
  running=$(/usr/local/bin/prism-phone-agent -version 2>/dev/null || true)
  if [ "$here" = "$there" ] && [ -z "$(git status --porcelain)" ] && [ "$running" = "$(git rev-parse --short=7 HEAD)" ]; then
    return 0
  fi
  if [ -n "$(git status --porcelain)" ]; then
    git stash push -u -q -m "auto-update $(date +%F_%T)：服务器上的手改，已存起来"
    log "服务器仓库有手改，已 git stash 保存（git stash list 可以看到）"
  fi
  git checkout -q "$BRANCH"
  git reset -q --hard "origin/$BRANCH"
  log "同步到 $(git log --oneline -1)"
  if bash host/install-agent.sh --no-pull >> "$LOG" 2>&1; then
    log "管家程序已更新"
  else
    log "管家程序更新失败（已自动换回旧程序），见上面的日志"
  fi
}

install_timer() {
  cat > /etc/systemd/system/prism-phone-auto-update.service <<EOF
[Unit]
Description=prism-phone 跟 GitHub 同步并更新管家程序
After=network-online.target

[Service]
Type=oneshot
ExecStart=/bin/bash $REPO/host/auto-update.sh run
EOF
  cat > /etc/systemd/system/prism-phone-auto-update.timer <<'EOF'
[Unit]
Description=每 10 分钟检查一次 GitHub

[Timer]
OnBootSec=2min
OnUnitActiveSec=10min
RandomizedDelaySec=30

[Install]
WantedBy=timers.target
EOF
  systemctl daemon-reload
  systemctl enable --now prism-phone-auto-update.timer
  log "已安装定时同步（每 10 分钟）"
}

case "${1:-}" in
  run) run ;;
  install) install_timer; run ;;
  status) systemctl list-timers prism-phone-auto-update.timer --no-pager 2>/dev/null; tail -n 10 "$LOG" 2>/dev/null || true ;;
  remove) systemctl disable --now prism-phone-auto-update.timer 2>/dev/null || true; log "已取消定时同步" ;;
  *) sed -n '2,14p' "$0"; exit 1 ;;
esac
