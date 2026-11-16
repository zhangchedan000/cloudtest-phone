#!/usr/bin/env bash
# 宿主机准备脚本：在一台通用 ARM64 Linux（Ubuntu/Debian）上装好跑安卓容器需要的东西。
# 可重复执行（已经装好的会跳过）。不重启、不改网络、不动 SSH。
#
# 用法： sudo bash host/setup-host.sh
set -euo pipefail

log() { printf '[setup] %s\n' "$*"; }
die() { printf '[setup] 错误：%s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "请用 root 运行"
[ "$(uname -m)" = aarch64 ] || die "只支持 ARM64（当前 $(uname -m)）"

# 1. 内存页必须 4K（64K 页 Redroid 起不来，例如 Oracle Linux 的内核）
pagesize=$(getconf PAGESIZE)
[ "$pagesize" = 4096 ] || die "内存页是 ${pagesize}，必须是 4096。换用 4K 页的内核/发行版（例如 Ubuntu）"

# 2. 等待系统自带的 apt 任务（自动更新）结束，避免抢锁。不强杀正在安装的进程。
wait_apt() {
  local n=0
  while fuser /var/lib/dpkg/lock-frontend /var/lib/apt/lists/lock >/dev/null 2>&1; do
    n=$((n+1)); [ $n -gt 120 ] && die "apt 被占用超过 10 分钟，先查是不是有卡死的 apt-get（见 docs/踩坑记录.md）"
    log "等待其他 apt 任务结束…"; sleep 5
  done
}

# 3. 软件包：docker、adb、jq、curl
need=()
command -v docker >/dev/null || need+=(docker.io)
command -v adb    >/dev/null || need+=(adb)
command -v jq     >/dev/null || need+=(jq)
command -v curl   >/dev/null || need+=(curl)
command -v lxcfs  >/dev/null || need+=(lxcfs)        # 手机里的 /proc/meminfo 按每台的内存上限显示（踩坑 #54）
command -v nsenter >/dev/null || need+=(util-linux)  # 实时画面进时间命名空间（踩坑 #50）
command -v mkfs.ext4 >/dev/null || need+=(e2fsprogs) # 按机型容量的手机存储镜像（踩坑 #53）
if [ ${#need[@]} -gt 0 ]; then
  wait_apt
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -o Acquire::http::Timeout=30
  apt-get install -y "${need[@]}"
fi
systemctl enable --now docker >/dev/null
# lxcfs 一重启，正在运行的手机里 /proc/meminfo 就读不了（挂载断开），所以不让它自动升级（升级挑没开手机的时候手动做）
systemctl enable --now lxcfs >/dev/null 2>&1 || true
apt-mark hold lxcfs >/dev/null 2>&1 || true

# 4. 内核功能：binder（binderfs 模式，每个容器自己挂载，互不冲突）、PSI
cfg="/boot/config-$(uname -r)"
check_cfg() { # $1=选项 $2=说明
  if [ -r "$cfg" ] && ! grep -Eq "^CONFIG_$1=(y|m)" "$cfg"; then die "内核缺少 CONFIG_$1（$2）"; fi
}
check_cfg ANDROID_BINDER_IPC "binder"
check_cfg ANDROID_BINDERFS "binderfs"
check_cfg PSI "否则 lmkd 反复重启"
if ! grep -qw binder /proc/filesystems; then
  modprobe binder_linux || die "加载 binder_linux 失败"
fi
echo binder_linux > /etc/modules-load.d/prism-phone.conf
grep -qw binder /proc/filesystems || die "binderfs 不可用"
[ -d /proc/pressure ] || die "PSI 没开（内核参数加 psi=1）"

# 5. 目录
mkdir -p /opt/prism-phone/{data,logs,images}

log "完成：docker $(docker --version | awk '{print $3}' | tr -d ,)，binderfs 可用，PSI 可用，内存页 4K"
