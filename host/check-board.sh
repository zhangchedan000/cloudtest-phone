#!/usr/bin/env bash
# 板子到手检查（只读，不改任何东西）：这台机器能不能直接跑云手机，要不要编译内核。
#
# 用法（root，在仓库目录里或者单独拷过去都行）：
#   bash host/check-board.sh
#
# 检查四类东西，逐项打 ✅ / ⚠️ / ❌，最后给结论：
#   1. 基本：架构、CPU / 板子型号、内存、硬盘、温度
#   2. 内核功能：Redroid 要的 binder 等（缺了就得用厂家 SDK 打开开关重新编译内核）
#   3. 显卡和视频编码：Mali 驱动、/dev/dri、瑞芯微 MPP 硬件编解码（缺了画面只能软件渲染）
#   4. 软件：Docker、nftables、网络模块要的 TPROXY 等
# 2026-11-15 写，先在甲骨文阿什本上跑通（那台没有显卡，第 3 类本来就会 ❌）。
set -u

pass=0; warn=0; failn=0; kernel_missing=(); gpu_missing=()
ok()   { echo "  ✅ $*"; pass=$((pass+1)); }
wa()   { echo "  ⚠️  $*"; warn=$((warn+1)); }
bad()  { echo "  ❌ $*"; failn=$((failn+1)); }
sect() { echo; echo "== $*"; }

# ---------- 1. 基本 ----------
sect "1. 基本情况"
arch=$(uname -m)
[ "$arch" = aarch64 ] && ok "架构 $arch" || bad "架构 $arch（云手机镜像是 ARM64 的，要 aarch64）"
compat=$( { tr '\0' ' ' < /proc/device-tree/compatible; } 2>/dev/null || true)
if echo "$compat" | grep -qi "rk3588"; then
  echo "$compat" | grep -qi "rk3588j" && wa "板子：$compat（RK3588J 是降频的工业版）" || ok "板子：$compat"
elif [ -n "$compat" ]; then
  wa "板子：$compat（不是 RK3588）"
else
  wa "读不到板子型号（/proc/device-tree/compatible 不存在，可能是云服务器）"
fi
cores=$(nproc)
parts=$(grep -i "CPU part" /proc/cpuinfo | awk '{print $NF}' | sort | uniq -c | tr -s ' ' | tr '\n' ' ')
echo "  ·  CPU：$cores 核（CPU part：$parts）"
mem_gb=$(awk '/MemTotal/{printf "%.0f", $2/1024/1024}' /proc/meminfo)
[ "$mem_gb" -ge 15 ] && ok "内存 ${mem_gb} GB" || wa "内存 ${mem_gb} GB（建议 ≥16 GB，一台云手机默认 3 GB）"
if ls /dev/nvme*n1 >/dev/null 2>&1; then ok "有 NVMe 固态：$(ls /dev/nvme*n1 | tr '\n' ' ')"; else wa "没看到 NVMe 固态（只用 eMMC / SD 卡会很慢）"; fi
free_gb=$(df -BG --output=avail / | tail -1 | tr -dc 0-9)
[ "$free_gb" -ge 50 ] && ok "系统盘剩余 ${free_gb} GB" || wa "系统盘剩余 ${free_gb} GB（每个安卓镜像 2 GB 多，每台手机数据几 GB）"
temps=""
for z in /sys/class/thermal/thermal_zone*/temp; do [ -r "$z" ] && temps="$temps $(( $(cat "$z") / 1000 ))°C"; done
[ -n "$temps" ] && echo "  ·  温度：$temps（空闲时超过 70°C 要加散热）" || echo "  ·  温度：读不到"

# ---------- 2. 内核功能 ----------
sect "2. 内核：$(uname -r)"
cfg=""
if [ -r /proc/config.gz ]; then cfg=$(zcat /proc/config.gz)
elif [ -r "/boot/config-$(uname -r)" ]; then cfg=$(cat "/boot/config-$(uname -r)")
fi
[ -n "$cfg" ] || wa "找不到内核配置（/proc/config.gz、/boot/config-*），下面只能按实际情况判断"
# kconf 名字 说明 必须(1)/建议(0)：=y 直接有；=m 是模块，看能不能加载
kconf() {
  local name=$1 desc=$2 must=$3 val
  val=$(echo "$cfg" | grep -E "^$name=" | cut -d= -f2)
  case "$val" in
    y) ok "$desc（$name=y）" ;;
    m) if [ -d "/lib/modules/$(uname -r)" ]; then ok "$desc（$name=m，是模块，用时加载）"
       else wa "$desc（$name=m 是模块，但 /lib/modules/$(uname -r) 不存在，加载不了）"; fi ;;
    *) if [ -z "$cfg" ]; then wa "$desc（$name：没有配置文件，没法确认）"
       elif [ "$must" = 1 ]; then bad "$desc（$name 没开）"; kernel_missing+=("$name")
       else wa "$desc（$name 没开）"; fi ;;
  esac
}
kconf CONFIG_ANDROID_BINDER_IPC   "binder（安卓进程之间通信，Redroid 必须）" 1
kconf CONFIG_ANDROID_BINDERFS     "binderfs（每台云手机独立的 binder，必须）" 1
kconf CONFIG_PSI                  "PSI（安卓内存回收用）" 0
kconf CONFIG_MEMFD_CREATE         "memfd（代替 ashmem）" 1
kconf CONFIG_INPUT_UINPUT        "uinput（虚拟触摸屏，以后做多点触控用）" 0
kconf CONFIG_OVERLAY_FS           "overlayfs（Docker 存储）" 1
kconf CONFIG_VETH                 "veth（Docker 网络）" 1
kconf CONFIG_BRIDGE               "bridge（Docker 网络）" 1
kconf CONFIG_MEMCG                "内存 cgroup（限制每台云手机内存）" 1
kconf CONFIG_NFT_TPROXY           "nft tproxy（网络模块：每台走自己的代理）" 1
kconf CONFIG_NFT_SOCKET           "nft socket（网络模块）" 0
kconf CONFIG_NETFILTER_XT_TARGET_TPROXY "iptables TPROXY（网络模块备用）" 0
kconf CONFIG_TIME_NS             "时间命名空间（云手机开机时长从 0 开始，不暴露宿主机开了多久；没有就照常开机、只是开机时长是宿主机的）" 0
if grep -qw binder /proc/filesystems; then ok "binderfs 已能挂载（/proc/filesystems 里有 binder）"
elif modprobe -n binder_linux 2>/dev/null; then wa "binderfs 要先加载模块 binder_linux（host/setup-host.sh 会处理）"
else wa "/proc/filesystems 里没有 binder"; fi
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then ok "cgroup v2"; else wa "不是 cgroup v2（Docker 也能用，资源限制可能不准）"; fi

# ---------- 3. 显卡和视频编码 ----------
sect "3. 显卡和硬件视频编码"
if [ -e /dev/mali0 ]; then ok "Mali 显卡设备 /dev/mali0"
else bad "没有 /dev/mali0（Mali 显卡驱动没加载，云手机只能软件渲染）"; gpu_missing+=("/dev/mali0"); fi
mali_ver=$(cat /sys/module/mali_kbase/version 2>/dev/null || cat /sys/module/bifrost_kbase/version 2>/dev/null || true)
[ -n "$mali_ver" ] && echo "  ·  Mali 驱动版本：$mali_ver"
if ls /dev/dri/renderD* >/dev/null 2>&1; then ok "DRM 渲染节点：$(ls /dev/dri/renderD* | tr '\n' ' ')"; else wa "没有 /dev/dri/renderD*"; fi
if [ -e /dev/mpp_service ]; then ok "瑞芯微 MPP 硬件编解码 /dev/mpp_service（实时画面可以用硬件编码）"
else bad "没有 /dev/mpp_service（实时画面只能软件编码，又慢又费 CPU）"; gpu_missing+=("/dev/mpp_service"); fi
[ -e /dev/rga ] && ok "RGA 图像加速 /dev/rga" || wa "没有 /dev/rga（缩放、格式转换用，没有也能跑）"
ls /dev/dma_heap/* >/dev/null 2>&1 && ok "dma_heap：$(ls /dev/dma_heap | tr '\n' ' ')" || wa "没有 /dev/dma_heap（显卡共享内存用）"

# ---------- 4. 软件 ----------
sect "4. 软件"
if command -v docker >/dev/null; then ok "Docker $(docker --version 2>/dev/null | awk '{print $3}' | tr -d ,)"; else wa "没装 Docker（host/setup-host.sh 会装）"; fi
command -v nft >/dev/null && ok "nftables $(nft --version 2>/dev/null | awk '{print $2}')" || wa "没装 nftables（host/network.sh 会装）"
ip rule list >/dev/null 2>&1 && ok "策略路由（ip rule）可用" || wa "ip rule 用不了"
[ "$(id -u)" = 0 ] || wa "不是 root 跑的，部分检查可能不准"
command -v adb >/dev/null && ok "adb" || wa "没装 adb（可选）"

# ---------- 结论 ----------
echo
echo "== 结论：✅ $pass 项，⚠️ $warn 项，❌ $failn 项"
if [ ${#kernel_missing[@]} -eq 0 ]; then
  echo "  · 内核：Redroid 必须的功能都在，不用编译内核。"
else
  echo "  · 内核：缺 ${kernel_missing[*]} → 需要用厂家 SDK 的内核源码打开这些开关重新编译一次（每种板子一次，脚本进仓库）。"
fi
if [ ${#gpu_missing[@]} -eq 0 ]; then
  echo "  · 显卡 / 视频编码：都在，管家程序可以加 -gpu-family Mali-G610 -gpu-mode host。"
else
  echo "  · 显卡 / 视频编码：缺 ${gpu_missing[*]} → 先确认厂家系统镜像是不是带了驱动；云服务器没有显卡，这一项本来就会缺。"
fi
echo "  · 下一步：bash host/setup-host.sh → bash host/install-agent.sh → bash host/itest-api.sh"
