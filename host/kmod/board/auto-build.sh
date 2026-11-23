#!/bin/sh
# 内核和仓库里预编译的内核模块对不上时（魔云腾升级了固件、换了一版内核），在板子上照正在跑的内核现编、核对、装上。
# install.sh 发现内核不一样会在后台自动调它；也可以手动跑。要联网（GitHub 拉 Rockchip 内核源码、Debian 装编译工具），
# 第一次要几十分钟（编整个内核，为了拿编译脚本和符号表）；数据都在 /mmc。
#
# 三步（每步都会核对，失败就停，不装半成品）：
#   build.sh          公开源码 + 板子的 /proc/config.gz 编一遍内核（拿编译环境和符号表）
#   build-kheaders.sh 换成板子内核自带的头文件（/sys/kernel/kheaders.tar.xz），核对关键函数的 CRC 和板子内核一致
#   build-modules.sh  编 wlan.ko / pp_uts.ko / pp_mnt.ko
# 编好装到 /opt/prism-phone/kmod，kernel.txt 写成正在跑的内核（管家 init.d 开机按它核对才加载），然后加载。
#
# 用法（root，板子上）：sh auto-build.sh [仓库提交，默认 main]
#   PP_KMOD_NOINSTALL=1：只编、只核对，不装（在内核本来就对得上的板子上演练用）
set -eu
REF=${1:-main}
REPO=${PP_REPO:-zhangchedan000/cloudtest-phone}
B=/mmc/pp-build
export PP_KMOD=$B/kmod-auto
SRC=$B/kmod-auto-repo
log(){ echo "$(date '+%F %T') [内核模块] $*"; }
die(){ log "✗ $*"; exit 1; }
[ "$(id -u)" = 0 ] || die "要 root"
ver=$(cut -d' ' -f3 /proc/version)
case "$ver" in 6.1.*) ;; *) die "只会编 6.1 系列的内核（这块板子是 $ver）" ;; esac
[ -r /sys/kernel/kheaders.tar.xz ] || die "内核没带自己的头文件（/sys/kernel/kheaders.tar.xz），没法编"
command -v git >/dev/null && command -v docker >/dev/null || die "要 git 和 docker"
mkdir -p $B
log "正在跑的内核：$(cat /proc/version)"
# 每次从干净的源码树开始（上次的源码树已经换成板子头文件了，再编内核会出错）；只留软件包下载缓存
[ -d "$PP_KMOD" ] && find "$PP_KMOD" -mindepth 1 -maxdepth 1 ! -name aptcache -exec rm -rf {} +
rm -rf "$SRC"
git clone -q --depth 1 --branch "$REF" "https://github.com/$REPO" "$SRC" 2>/dev/null ||
  { git clone -q "https://github.com/$REPO" "$SRC" && (cd "$SRC" && git checkout -q "$REF"); } || die "拉仓库失败"
cd "$SRC"
t0=$(date +%s)
log "1/3 编内核（拿编译环境，要几十分钟）"
sh host/kmod/board/build.sh > $B/kmod-auto-1.log 2>&1 || { tail -20 $B/kmod-auto-1.log; die "编内核失败（$B/kmod-auto-1.log）"; }
log "2/3 换板子内核的头文件、核对 CRC"
sh host/kmod/board/build-kheaders.sh > $B/kmod-auto-2.log 2>&1 || { tail -20 $B/kmod-auto-2.log; die "头文件核对失败（$B/kmod-auto-2.log）"; }
log "3/3 编模块"
sh host/kmod/board/build-modules.sh > $B/kmod-auto-3.log 2>&1 || { tail -20 $B/kmod-auto-3.log; die "编模块失败（$B/kmod-auto-3.log）"; }
for m in wlan pp_uts pp_mnt; do
  [ -s "$PP_KMOD/out/$m.ko" ] || die "没编出 $m.ko"
  vm=$(tr '\0' '\n' < "$PP_KMOD/out/$m.ko" | sed -n 's/^vermagic=//p' | head -1 | cut -d' ' -f1)   # busybox 的 modinfo 读不出
  [ "$vm" = "$ver" ] || die "$m.ko 的内核版本 $vm 和正在跑的 $ver 对不上"
done
log "编好了（$(( $(date +%s) - t0 )) 秒）：$(cd $PP_KMOD/out && sha256sum *.ko | cut -c1-16 | tr '\n' ' ')"
[ "${PP_KMOD_NOINSTALL:-0}" = 1 ] && { log "PP_KMOD_NOINSTALL=1：不装"; exit 0; }
log "装到 /opt/prism-phone/kmod"
mkdir -p /opt/prism-phone/kmod
for m in wlan pp_uts pp_mnt; do install -m 644 "$PP_KMOD/out/$m.ko" /opt/prism-phone/kmod/; done
cat /proc/version > /opt/prism-phone/kmod/kernel.txt
# 有手机在跑时不换已经加载的（换了模块里「哪台手机的设备 / 端口」的记录会丢）；没加载的直接加载
if [ "$(docker ps -q | wc -l)" = 0 ]; then
  for m in pp_mnt pp_uts wlan; do [ -d /sys/module/$m ] && rmmod $m 2>/dev/null || true; done
fi
rc-service prism-phone-agent kmods >/dev/null 2>&1 || true
# 功能自检：三个模块都加载上、两个 /proc 接口在、两道保护（容器写 sysrq、停用宿主机的盘）的钩子挂上了
bad=0
for m in wlan pp_uts pp_mnt; do [ -d /sys/module/$m ] && log "✓ $m 已加载" || { log "✗ $m 没加载上（看 dmesg）"; bad=1; }; done
[ -e /proc/pp_mnt ] && [ -e /proc/pp_uts ] && log "✓ /proc/pp_mnt、/proc/pp_uts 在" || { log "✗ /proc/pp_mnt 或 /proc/pp_uts 不在"; bad=1; }
if dmesg | grep -qE 'pp_mnt: 没挂上 (write_sysrq_trigger|ext4_ioctl)'; then log "✗ 保护钩子没挂上：$(dmesg | grep -oE 'pp_mnt: 没挂上 [a-z0-9_]+' | sort -u | tr '\n' ' ')"; bad=1; else log "✓ 两道保护（sysrq、停用盘）钩子已挂上"; fi
[ $bad = 0 ] || die "功能自检没过：先别开手机，把 /mmc/pp-build/kmod-auto*.log 和 dmesg 发给开发看"
log "✓ 全部好了"
