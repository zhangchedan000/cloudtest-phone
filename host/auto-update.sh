#!/usr/bin/env bash
# 服务器跟 GitHub 自动同步：GitHub 上的 main 为准，有新提交就拉下来、编译、更新管家程序。
#
# 用法（root，在仓库目录里）：
#   bash host/auto-update.sh install   # 装成 systemd 定时任务：每天北京时间 4 点更新一次；网络规则每 10 分钟检查一次
#   bash host/auto-update.sh run       # 立刻检查并更新（要马上上线修复时手动跑这个）
#   bash host/auto-update.sh heal      # 只检查网络规则（10 分钟的定时任务调用的就是这个，不更新程序）
#   bash host/auto-update.sh status    # 看上次同步结果
#   bash host/auto-update.sh remove    # 取消定时任务
#
# 更新时间（2026-11-17 用户要求）：以前每 10 分钟有新提交就更新，管家重启时管理页会断几秒；
# 现在每天一次（PP_UPDATE_AT，默认北京时间 04:00，那时一般没人用），白天操作手机不会被打断。手机本身不受更新影响。
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

heal() {
  # 检查网络模块的策略路由还在不在（踩坑 #34：被 systemd-networkd 删过，云手机全部断网）
  local healed
  healed=$(bash "$REPO/host/network.sh" heal 2>&1 || true)
  [ -n "$healed" ] && log "$healed"
  return 0
}

run() {
  cd "$REPO"
  exec 9>/run/prism-phone-auto-update.lock
  flock -n 9 || { log "上一次同步还没做完，跳过"; return 0; }
  heal
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
  # 有手机正在开机、做开机后设置时先不更新（重启管家会打断设置，踩坑 #45）：一天只更新一次，所以等它做完（最多 30 分钟）。
  # 管家没在跑 / 老版本没有 busy 字段时照常更新。
  local listen
  listen=$(sed -n 's/^AGENT_ARGS=.*-listen[ =]\([^ ]*\).*/\1/p' /etc/prism-phone/agent.env 2>/dev/null | head -1)
  listen=${listen:-127.0.0.1:8099}
  case "$listen" in 0.0.0.0:*|:*) listen="127.0.0.1:${listen##*:}" ;; esac
  local i
  for i in $(seq 1 30); do
    curl -s -m 3 "http://$listen/api/health" 2>/dev/null | grep -q '"busy":true' || break
    [ "$i" = 1 ] && log "有手机正在做开机后设置，等它做完再更新"
    sleep 60
  done
  if curl -s -m 3 "http://$listen/api/health" 2>/dev/null | grep -q '"busy":true'; then
    log "等了 30 分钟还在忙，这次先不更新（明天再更新，或手动 bash host/auto-update.sh run）"
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

UPDATE_AT=${PP_UPDATE_AT:-04:00 Asia/Shanghai}

install_timer() {
  cat > /etc/systemd/system/prism-phone-auto-update.service <<UNIT
[Unit]
Description=prism-phone 跟 GitHub 同步并更新管家程序
After=network-online.target

[Service]
Type=oneshot
Environment=HOME=/root
ExecStart=/bin/bash $REPO/host/auto-update.sh run
UNIT
  cat > /etc/systemd/system/prism-phone-auto-update.timer <<UNIT
[Unit]
Description=每天 $UPDATE_AT 更新一次管家程序

[Timer]
OnCalendar=*-*-* $UPDATE_AT
Persistent=true
RandomizedDelaySec=120

[Install]
WantedBy=timers.target
UNIT
  # 网络规则检查还是 10 分钟一次（只修网络、不更新程序、不重启管家）
  cat > /etc/systemd/system/prism-phone-net-heal.service <<UNIT
[Unit]
Description=prism-phone 检查网络模块的路由规则（被删了就补回）
After=network-online.target

[Service]
Type=oneshot
ExecStart=/bin/bash $REPO/host/auto-update.sh heal
UNIT
  cat > /etc/systemd/system/prism-phone-net-heal.timer <<'UNIT'
[Unit]
Description=每 10 分钟检查一次网络规则

[Timer]
OnBootSec=2min
OnUnitActiveSec=10min
RandomizedDelaySec=30

[Install]
WantedBy=timers.target
UNIT
  systemctl daemon-reload
  systemctl enable --now prism-phone-net-heal.timer
  systemctl enable prism-phone-auto-update.timer
  systemctl restart prism-phone-auto-update.timer
  log "已安装定时任务：每天 $UPDATE_AT 更新管家程序；每 10 分钟检查网络规则"
}

case "${1:-}" in
  run) run ;;
  heal) cd "$REPO"; heal ;;
  install) install_timer ;;
  status) systemctl list-timers prism-phone-auto-update.timer prism-phone-net-heal.timer --no-pager 2>/dev/null; tail -n 10 "$LOG" 2>/dev/null || true ;;
  remove) systemctl disable --now prism-phone-auto-update.timer prism-phone-net-heal.timer 2>/dev/null || true; log "已取消定时同步" ;;
  *) sed -n '2,18p' "$0"; exit 1 ;;
esac
