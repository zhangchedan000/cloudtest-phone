#!/bin/sh
# 板子（魔云腾 Q1 / Alpine + OpenRC）一键安装 / 更新：管家程序 + host/board 下的系统配置。
#
# 用法（板子上 root，不需要 clone 仓库）：
#   wget -qO /tmp/pp-install.sh https://raw.githubusercontent.com/zhangchedan000/cloudtest-phone/main/host/board/install.sh
#   sh /tmp/pp-install.sh            # 装 / 更新到 GitHub main 最新
#   sh /tmp/pp-install.sh 6eb3166    # 装指定提交（回到某个版本也用这个）
#   sh /tmp/pp-install.sh --check    # 只体检，不改任何东西
#
# 做的事：体检必需软件包（nftables 等，踩坑 #120）→ 备份要动的文件 → 从 GitHub 下载并校验 → 只替换有变化的文件
# → 只重启受影响的服务 → 自检；管家起不来自动用备份换回去。
# 不碰：SSH、Desktop Commander、mihomo 程序和代理配置、安卓镜像、手机数据。
# 有手机在跑时不重启 docker（会把手机全停掉），只提示；管家重启不影响正在跑的手机。
# 日志：/mmc/pp-install.log；备份：/mmc/pp-save/board-bak-时间.tgz（tar xzf 备份 -C / 即恢复）
set -u

REPO=${PP_REPO:-zhangchedan000/cloudtest-phone}
REF=main
CHECK_ONLY=0
case "${1:-}" in
  --check) CHECK_ONLY=1 ;;
  -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
  "") ;;
  *) REF=$1 ;;
esac
RAW="https://raw.githubusercontent.com/$REPO/$REF"
LOG=/mmc/pp-install.log
DL=/mmc/pp-install-dl
BAK=/mmc/pp-save/board-bak-$(date +%Y%m%d-%H%M%S).tgz

[ "$(id -u)" = 0 ] || { echo "请用 root 运行"; exit 1; }
[ -d /mmc ] || { echo "没有 /mmc 大盘（这个脚本是给魔云腾 Q1 板子用的）"; exit 1; }
command -v rc-service >/dev/null || { echo "不是 OpenRC 系统（这个脚本只给 Alpine 板子用；Ubuntu 服务器用 host/install-agent.sh）"; exit 1; }
# 输出同时写日志（busybox sh 没有进程替换：自己再跑一遍、经 tee 输出）
if [ -z "${PP_INSTALL_TEED:-}" ]; then
  PP_INSTALL_TEED=1 sh "$0" "$@" 2>&1 | tee -a "$LOG"
  exit 0
fi
say() { echo "$(date '+%F %T') $*"; }
die() { say "✗ $*"; exit 1; }

# 仓库里的路径 → 板子上的路径 → 权限
FILES="
host/board/etc/init.d/prism-phone-agent:/etc/init.d/prism-phone-agent:755
host/board/etc/init.d/prism-phone-fan:/etc/init.d/prism-phone-fan:755
host/board/etc/init.d/prism-phone-oled:/etc/init.d/prism-phone-oled:755
host/board/etc/init.d/prism-phone-mihomo:/etc/init.d/prism-phone-mihomo:755
host/board/etc/conf.d/docker:/etc/conf.d/docker:644
host/board/etc/conf.d/prism-phone-agent.example:/etc/conf.d/prism-phone-agent.example:644
host/board/etc/logrotate.d/prism-phone:/etc/logrotate.d/prism-phone:644
host/board/etc/sysctl.d/98-pp-scale.conf:/etc/sysctl.d/98-pp-scale.conf:644
host/board/etc/sysctl.d/99-pp-redir.conf:/etc/sysctl.d/99-pp-redir.conf:644
host/board/etc/local.d/binder-nodes.start:/etc/local.d/binder-nodes.start:755
host/board/etc/local.d/pp-redir.start:/etc/local.d/pp-redir.start:755
host/board/usr/local/bin/fan-control.sh:/usr/local/bin/fan-control.sh:755
"
AGENT=/opt/prism-phone/prism-phone-agent

# ---------- 体检 ----------
check() {
  bad=0
  for p in nftables logrotate curl; do
    apk info -e "$p" >/dev/null 2>&1 && say "✓ 软件包 $p" || { say "✗ 缺软件包 $p"; bad=1; }
  done
  nft list table ip prism_redir >/dev/null 2>&1 && say "✓ 网络规则表 prism_redir" || { say "✗ 网络规则表 prism_redir 不在（手机流量不走代理！）"; bad=1; }
  dl=$(sed -n '/^dns:/,/^[a-z]/s/^  listen: *"*\([0-9.]*:[0-9]*\)"*/\1/p' /mmc/pp-data/mihomo/config.yaml 2>/dev/null | head -1)
  if [ -n "$dl" ]; then
    netstat -lnu 2>/dev/null | grep -q " $dl " && say "✓ 手机的 DNS（$dl）在监听" || { say "✗ 手机的 DNS（$dl）没在监听（手机解析不了域名！rc-service prism-phone-mihomo restart，踩坑 #131）"; bad=1; }
  fi
  for s in docker prism-phone-agent prism-phone-mihomo prism-phone-fan; do
    rc-service "$s" status >/dev/null 2>&1 && say "✓ 服务 $s 在跑" || { say "✗ 服务 $s 没在跑"; bad=1; }
    rc-update show default 2>/dev/null | grep -q "^ *$s " || { say "✗ 服务 $s 没加开机自启"; bad=1; }
  done
  pgrep -f '^/bin/sh /usr/local/bin/fan-control.sh' >/dev/null && say "✓ 风扇脚本在跑" || { say "✗ 风扇脚本没在跑（满载会过热）"; bad=1; }
  [ -x /opt/pp-rootcanal/pp-rootcanal ] && [ -x /usr/local/bin/pp-rootcanal ] && say "✓ 蓝牙芯片模拟器" || say "· 没装蓝牙芯片模拟器（手机蓝牙关着；包放 /mmc/pp-dist/pp-rootcanal-arm64-bundle.tgz 再跑一次）"
  if [ -d /sys/module/mac80211_hwsim ]; then say "✓ 虚拟 WiFi 驱动已加载"; else say "· 虚拟 WiFi 驱动没加载（手机走有线网 eth0；只有魔云腾 Q1 这版内核有现成驱动，踩坑 #129）"; fi
  case " $(sed -n 's/^PP_ARGS="\(.*\)"$/\1/p' /etc/conf.d/prism-phone-agent 2>/dev/null) " in *" -no-wifi "*) say "· 管家带着 -no-wifi（手机走有线网）";; esac
  netstat -tln 2>/dev/null | grep -q ':2375 ' && { say "✗ docker 2375 端口开着"; bad=1; } || say "✓ docker 2375 已关"
  [ -f /etc/conf.d/prism-phone-agent ] && [ "$(stat -c %a /etc/conf.d/prism-phone-agent)" = 600 ] && say "✓ 令牌文件权限 600" || { say "✗ /etc/conf.d/prism-phone-agent 不在或权限不是 600"; bad=1; }
  c=$(curl -s -m3 -o /dev/null -w '%{http_code}' 127.0.0.1:8099/api/health)
  [ "$c" = 200 ] && say "✓ 管家接口正常" || { say "✗ 管家接口不通（$c）"; bad=1; }
  free=$(df -m / | awk 'NR==2{print $4}')
  [ "$free" -gt 500 ] && say "✓ 根分区剩 ${free}M" || { say "✗ 根分区只剩 ${free}M（<500M，踩坑 #119）"; bad=1; }
  say "温度 $(( $(cat /sys/class/thermal/thermal_zone0/temp) / 1000 ))℃；管家版本 $($AGENT -version 2>/dev/null || echo ?)；运行中手机 $(docker ps -q 2>/dev/null | wc -l) 台"
  return $bad
}

if [ "$CHECK_ONLY" = 1 ]; then
  check && say "体检全部通过" || say "体检有问题，见上面 ✗"
  exit 0
fi

say "===== 安装 / 更新 $REPO@$REF ====="
free=$(df -m / | awk 'NR==2{print $4}')
[ "$free" -gt 300 ] || die "根分区只剩 ${free}M，先清理（踩坑 #119）"

# ---------- 1. 必需软件包 ----------
for p in nftables logrotate; do
  apk info -e "$p" >/dev/null 2>&1 || { say "装软件包 $p"; apk add -q "$p" || die "apk add $p 失败"; }
done

# ---------- 2. 下载 + 校验 ----------
rm -rf "$DL"; mkdir -p "$DL"
fetch() { curl -fsSL --retry 3 -m 300 -o "$DL/$2" "$RAW/$1" || die "下载失败：$1"; }
for line in $FILES; do
  src=${line%%:*}; fetch "$src" "$(echo "$src" | tr / _)"
done
fetch tools/dist/prism-phone-agent-arm64.gz agent.gz
fetch tools/dist/prism-phone-agent-arm64.gz.sha256 agent.gz.sha256
want=$(awk '{print $1}' "$DL/agent.gz.sha256")
got=$(sha256sum "$DL/agent.gz" | awk '{print $1}')
[ -n "$want" ] && [ "$want" = "$got" ] || die "管家程序校验失败（下载不完整或被改过）：要 $want 实际 $got"
gunzip -c "$DL/agent.gz" > "$DL/agent" && chmod 755 "$DL/agent"
"$DL/agent" -version >/dev/null 2>&1 || die "新管家程序跑不起来（-version 失败）"
for f in "$DL"/*.start "$DL"/*_init.d_* "$DL"/*fan-control.sh; do sh -n "$f" || die "脚本语法错：$f"; done
say "下载完成，新管家版本 $("$DL/agent" -version)"

# ---------- 3. 备份 ----------
mkdir -p /mmc/pp-save
list="$AGENT"
for line in $FILES; do dst=$(echo "$line" | cut -d: -f2); [ -e "$dst" ] && list="$list $dst"; done
[ -e /etc/conf.d/prism-phone-agent ] && list="$list /etc/conf.d/prism-phone-agent"
# shellcheck disable=SC2086
tar czf "$BAK" $list 2>/dev/null || die "备份失败"
say "备份：$BAK"

# ---------- 4. 令牌和参数（/etc/conf.d/prism-phone-agent） ----------
CONF=/etc/conf.d/prism-phone-agent
if [ ! -f "$CONF" ]; then
  old=/etc/init.d/prism-phone-agent
  tok=$(sed -n 's/^export PP_TOKEN="\(.*\)"$/\1/p' "$old" 2>/dev/null)
  args=$(sed -n 's/^command_args="\([^$].*\)"$/\1/p' "$old" 2>/dev/null)
  if [ "${#tok}" -ge 16 ] && [ "$tok" != "改成你自己的令牌" ] && [ -n "$args" ]; then
    (umask 077; printf 'PP_TOKEN="%s"\nPP_ARGS="%s"\n' "$tok" "$args" > "$CONF")
    say "令牌和参数从旧启动脚本挪到 $CONF"
  else
    (umask 077; cp "$DL/host_board_etc_conf.d_prism-phone-agent.example" "$CONF")
    die "第一次装：已生成 $CONF，请改里面的 PP_TOKEN（至少 16 位）后再运行一次"
  fi
fi
chmod 600 "$CONF"

# ---------- 5. 替换有变化的文件 ----------
changed=""
for line in $FILES; do
  src=${line%%:*}; dst=$(echo "$line" | cut -d: -f2); mode=${line##*:}
  new="$DL/$(echo "$src" | tr / _)"
  if ! cmp -s "$new" "$dst"; then
    mkdir -p "$(dirname "$dst")"; install -m "$mode" "$new" "$dst"; changed="$changed $dst"; say "更新 $dst"
  fi
done
agent_changed=0
if ! cmp -s "$DL/agent" "$AGENT"; then
  [ -f "$AGENT" ] && cp -a "$AGENT" "$AGENT.prev"
  install -m 755 "$DL/agent" "$AGENT"; agent_changed=1; say "更新管家程序"
fi
has() { case "$changed " in *" $1 "*) return 0 ;; esac; return 1; }

# ---------- 5.5 蓝牙芯片模拟器（踩坑 #122）：有包没装就装；没包就不装（管家会把蓝牙关着） ----------
RCB=/mmc/pp-dist/pp-rootcanal-arm64-bundle.tgz
RCB_SHA=46d384c9a2104f128c9efe354abad905072661e3729556f0991a297cdcad62c6
# 本地没有就从仓库 tools/dist 取（20M 以内的复用件放仓库，换板不怕老板子坏）
if [ ! -f "$RCB" ] && [ ! -x /opt/pp-rootcanal/pp-rootcanal ] && [ -d /mmc ]; then
  mkdir -p /mmc/pp-dist
  if curl -fsSL --retry 3 -m 300 -o "$RCB.part" "$RAW/tools/dist/pp-rootcanal-arm64-bundle.tgz" 2>/dev/null &&
     [ "$(sha256sum "$RCB.part" | cut -d' ' -f1)" = "$RCB_SHA" ]; then
    mv "$RCB.part" "$RCB" && say "从仓库取到蓝牙芯片模拟器包"
  else
    rm -f "$RCB.part"
  fi
fi
if [ -f "$RCB" ] && [ ! -x /opt/pp-rootcanal/pp-rootcanal ]; then
  tar xzf "$RCB" -C /opt && say "装蓝牙芯片模拟器（/opt/pp-rootcanal）"
fi
if [ -x /opt/pp-rootcanal/pp-rootcanal ]; then
  fetch host/board/usr/local/bin/pp-rootcanal pp-rootcanal.wrapper
  cmp -s "$DL/pp-rootcanal.wrapper" /usr/local/bin/pp-rootcanal || { install -m 755 "$DL/pp-rootcanal.wrapper" /usr/local/bin/pp-rootcanal; say "更新 /usr/local/bin/pp-rootcanal"; }
fi

# ---------- 5.6 虚拟 WiFi 驱动 mac80211_hwsim（踩坑 #129）：只给【和编它时一模一样的内核】装 ----------
# 魔云腾内核没带这个驱动；仓库里的 .ko 是按 Q1 这版内核自带的头文件编的，换了内核（哪怕版本号一样）结构可能不同，不能装。
KMOD_DIR=tools/dist/kmod/mytos-q1-6.1.141
KMOD_KERNEL="Linux version 6.1.141 (root@SWDM8607) (clang version 10.0.6 for Android NDK) #278 SMP PREEMPT Mon Oct 19 06:29:17 UTC 2026"
if [ "$(cat /proc/version 2>/dev/null)" = "$KMOD_KERNEL" ]; then
  fetch "$KMOD_DIR/mac80211_hwsim.ko" hwsim.ko
  fetch "$KMOD_DIR/mac80211_hwsim.ko.sha256" hwsim.ko.sha256
  if [ "$(sha256sum "$DL/hwsim.ko" | cut -d' ' -f1)" = "$(cut -d' ' -f1 "$DL/hwsim.ko.sha256")" ]; then
    mkdir -p /opt/prism-phone/kmod
    cmp -s "$DL/hwsim.ko" /opt/prism-phone/kmod/mac80211_hwsim.ko || { install -m 644 "$DL/hwsim.ko" /opt/prism-phone/kmod/mac80211_hwsim.ko; say "装虚拟 WiFi 驱动（/opt/prism-phone/kmod/mac80211_hwsim.ko）"; }
    printf '%s\n' "$KMOD_KERNEL" > /opt/prism-phone/kmod/kernel.txt   # 管家 init.d 开机加载前按它核对内核
    [ -d /sys/module/mac80211_hwsim ] || { insmod /opt/prism-phone/kmod/mac80211_hwsim.ko radios=0 && say "加载虚拟 WiFi 驱动"; }
  else
    say "· 虚拟 WiFi 驱动校验不对，没装（手机照旧用有线网）"
  fi
  apk info -e hostapd >/dev/null 2>&1 && apk info -e iw >/dev/null 2>&1 && apk info -e dnsmasq >/dev/null 2>&1 && apk info -e iproute2 >/dev/null 2>&1 || { apk add -q iw hostapd dnsmasq iproute2 && say "装 WiFi 用的 iw / hostapd / dnsmasq / iproute2"; }
fi

# ---------- 6. 生效 ----------
sysctl -q -p /etc/sysctl.d/98-pp-scale.conf; sysctl -q -p /etc/sysctl.d/99-pp-redir.conf
[ -e /dev/vndbinder ] || sh /etc/local.d/binder-nodes.start
if has /etc/local.d/pp-redir.start || ! nft list table ip prism_redir >/dev/null 2>&1; then
  sh /etc/local.d/pp-redir.start && say "网络规则已加载" || die "网络规则加载失败"
fi
for s in docker prism-phone-fan prism-phone-mihomo prism-phone-agent prism-phone-oled; do
  [ -f "/etc/init.d/$s" ] && { rc-update show default | grep -q "^ *$s " || rc-update add "$s" default; }
done
if has /etc/init.d/prism-phone-fan || has /usr/local/bin/fan-control.sh; then
  rc-service prism-phone-fan stop >/dev/null 2>&1; pkill -f '^/bin/sh /usr/local/bin/fan-control.sh'; sleep 1
  rc-service prism-phone-fan start
fi
running=$(docker ps -q | wc -l)
if has /etc/conf.d/docker; then
  if [ "$running" = 0 ]; then rc-service docker restart; sleep 3
  else say "⚠ docker 配置改了，但有 $running 台手机在跑，没重启 docker；手机都关了以后执行 rc-service docker restart"; fi
fi
has /etc/init.d/prism-phone-mihomo && rc-service prism-phone-mihomo restart
if [ "$agent_changed" = 1 ] || has /etc/init.d/prism-phone-agent; then
  rc-service prism-phone-agent restart >/dev/null 2>&1 || rc-service prism-phone-agent start
fi
rc-service prism-phone-agent status >/dev/null 2>&1 || rc-service prism-phone-agent start
has /etc/init.d/prism-phone-oled && rc-service prism-phone-oled restart

# ---------- 7. 管家起没起来，起不来就回滚 ----------
ok=0; i=0
while [ $i -lt 40 ]; do
  [ "$(curl -s -m2 -o /dev/null -w '%{http_code}' 127.0.0.1:8099/api/health)" = 200 ] && { ok=1; break; }
  sleep 1; i=$((i + 1))
done
if [ "$ok" != 1 ]; then
  say "✗ 管家 40 秒内没起来，用备份换回去"
  rc-service prism-phone-agent stop >/dev/null 2>&1
  tar xzf "$BAK" -C / && rc-service prism-phone-agent start
  die "已回滚到 $BAK；看 /var/log/pp-agent.log 找原因"
fi
say "管家已起来：$($AGENT -version)"
check && say "===== 完成，体检全部通过 =====" || say "===== 完成，但体检有问题，见上面 ✗ ====="
