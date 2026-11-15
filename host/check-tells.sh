#!/usr/bin/env bash
# 破绽自检（只读，不改任何东西）：进一台云手机容器，把「一个装在手机里的 App 能看出这不是真手机」的地方逐项列出来。
#
# 用途：开发测试。搞清楚每一项现在是什么状态、能不能现在在这台机器上解决、还是要等有显卡的板子。
# 不是为了骗过谁，只是把容器和真机的差别摸清楚。
#
# 用法（root，在仓库目录里）：
#   bash host/check-tells.sh [手机ID 或 容器名]
#   不给参数就自动挑第一台正在运行的云手机（pp-* 容器）。
#
# 每项前面的标签：
#   [已遮住]  管家程序的 fakefs 已经改了这项的读取结果，验证它有没有生效
#   [现在可做] 纯软件 / 配置问题，这台甲骨文机器上就能改
#   [等板子]  硬件缺的（显卡、传感器、摄像头等），和显卡一个性质，要等 RK3588 板子
set -u

# ---------- 选容器 ----------
arg=${1:-}
if [ -n "$arg" ]; then
  case "$arg" in pp-*) C=$arg ;; *) C="pp-$arg" ;; esac
else
  C=$(docker ps --filter "name=^pp-" --format '{{.Names}}' | head -1)
fi
if [ -z "${C:-}" ]; then
  echo "没有正在运行的云手机容器。先在管理页开一台，或者 bash host/check-tells.sh <手机ID>"; exit 1
fi
if ! docker inspect "$C" >/dev/null 2>&1; then echo "找不到容器 $C"; exit 1; fi
if [ "$(docker inspect -f '{{.State.Running}}' "$C")" != true ]; then echo "容器 $C 没在运行，先开机"; exit 1; fi
echo "自检容器：$C"

# ---------- 小工具 ----------
hide=0; nowfix=0; board=0
ax()  { docker exec "$C" sh -c "$1" 2>/dev/null; }                 # 在安卓容器里跑
gp()  { docker exec "$C" getprop "$1" 2>/dev/null; }
hid() { echo "  [已遮住]  $*"; hide=$((hide+1)); }
now() { echo "  [现在可做] $*"; nowfix=$((nowfix+1)); }
brd() { echo "  [等板子]  $*"; board=$((board+1)); }
info(){ echo "  ·        $*"; }
sect(){ echo; echo "== $*"; }

# ========== 1. fakefs 已经遮住的，验证有没有生效 ==========
sect "1. 已经处理的（验证 fakefs 生效）"
pv=$(ax 'cat /proc/version')
info "/proc/version：$pv"
echo "$pv" | grep -qiE 'oracle|generic|aws' && now "/proc/version 还露着宿主机内核字样，检查 fakefs 是否覆盖这台" || hid "/proc/version 已按机型改写"
cpuhw=$(ax 'grep -m1 Hardware /proc/cpuinfo'); info "cpuinfo Hardware：${cpuhw:-（无此行）}"
midr=$(ax 'cat /sys/devices/system/cpu/cpu0/regs/identification/midr_el1'); info "cpu0 midr_el1：${midr:-读不到}"
[ -n "$midr" ] && hid "CPU 型号寄存器 midr_el1 有值（按机型写的）" || now "midr_el1 读不到，确认 fakefs 挂载"

# ========== 2. 系统 / 容器特征（现在就能在这台机器上处理）==========
sect "2. 系统 / 容器特征（现在可做）"

# ro.hardware：Redroid 容器经常直接写着 redroid，等于明牌
rohw=$(gp ro.hardware); info "ro.hardware = ${rohw:-空}"
echo "$rohw" | grep -qiE 'redroid|ranchu|goldfish|qemu' && brd "ro.hardware=$rohw 暴露是容器。启动参数改它会开不了机（实测），要编译镜像时连驱动文件名一起改——归到板子/镜像阶段" || info "ro.hardware 看起来已按机型设置"

# qemu / 模拟器属性
for p in ro.kernel.qemu ro.boot.qemu init.svc.qemu-props ro.kernel.android.qemud; do
  v=$(gp "$p"); [ -n "$v" ] && now "残留模拟器属性 $p=$v，应清掉"
done

# ro.build.fingerprint / 各 product 属性是否自洽
fp=$(gp ro.build.fingerprint); info "ro.build.fingerprint = ${fp:-空}"
brand=$(gp ro.product.brand); model=$(gp ro.product.model); dev=$(gp ro.product.device)
info "brand/model/device = ${brand}/${model}/${dev}"
echo "$fp" | grep -q "$dev" 2>/dev/null || now "fingerprint 里不含 device=$dev，机型属性之间可能不自洽，核对一遍"
bootimg=$(gp ro.bootimage.build.fingerprint); vendfp=$(gp ro.vendor.build.fingerprint)
[ -n "$bootimg$vendfp" ] && { [ "$bootimg" = "$fp" ] && [ "$vendfp" = "$fp" ] || now "boot/vendor 的 fingerprint 和主 fingerprint 不一致，App 会比对（system/vendor/boot 应一致）"; }

# 内核：App 直接调 uname() 拿到的是宿主机内核（fakefs 只改文件视图，改不了系统调用）
unamer=$(ax 'uname -r'); info "uname -r（系统调用）：$unamer"
now "uname() 系统调用返回宿主机内核 $unamer，fakefs 只改 /proc 文件、改不了这个；真机上是对应机型内核，这是容器的硬伤之一"

# /proc/mounts：overlay、docker、fakefs 自己的 bind mount 都会露
mnt=$(ax 'cat /proc/mounts')
echo "$mnt" | grep -qi overlay && now "/proc/mounts 有 overlay（容器文件系统特征）"
echo "$mnt" | grep -qiE '/dev/null .* (cpuinfo|midr|version|osrelease)' && now "fakefs 用 bind mount 盖文件，/proc/mounts 里能看到这些挂载点本身（遮了内容、露了手法）"
bindcnt=$(echo "$mnt" | grep -cE 'cpuinfo|midr_el1|osrelease|/proc/version'); [ "$bindcnt" -gt 0 ] && info "可疑 bind mount 行数：$bindcnt"

# cgroup：容器路径
cg=$(ax 'cat /proc/1/cgroup'); echo "$cg" | grep -qiE 'docker|containerd|/pp-' && now "/proc/1/cgroup 露出 docker/containerd 路径"

# SELinux：容器里常是 Permissive，真机是 Enforcing
se=$(ax 'getenforce'); info "SELinux：${se:-读不到}"
[ "$se" = Enforcing ] || brd "SELinux=$se（真机是 Enforcing）。状态由宿主机内核决定，这台 Ubuntu 没开 SELinux，容器里改不动——要在有 SELinux 的板子上处理"

# root / 调试开关
dbg=$(gp ro.debuggable); sec=$(gp ro.secure); info "ro.debuggable=$dbg  ro.secure=$sec"
[ "$dbg" = 1 ] && now "ro.debuggable=1（真机出厂是 0）"
[ "$sec" = 0 ] && now "ro.secure=0（真机是 1）"
ax 'command -v su >/dev/null && echo yes' | grep -q yes && now "容器里有 su（root 可见），App 能检测到已 root"
vbs=$(gp ro.boot.verifiedbootstate); info "ro.boot.verifiedbootstate=${vbs:-空}"
[ "$vbs" = green ] || now "verifiedbootstate=${vbs:-空}（真机锁 bootloader 是 green），Play Integrity 之类会看"

# adb 状态
adbp=$(gp service.adb.tcp.port); [ -n "$adbp" ] && info "service.adb.tcp.port=$adbp（内部用，别对外）"

# 网卡：veth + 10.88 内网
ifs=$(ax 'ip -o addr show 2>/dev/null || cat /proc/net/fib_trie 2>/dev/null | head')
echo "$ifs" | grep -qE '10\.88\.|veth|eth0@' && now "网卡是容器虚拟网卡 / 10.88 内网段（真机是 wlan0 + 运营商/家庭 IP）"
echo "$ifs" | grep -qi wlan0 || now "没有 wlan0 无线网卡（真机一定有 WiFi 接口）"

# 屏幕密度：和机型档案对不对得上（文档里标过待核实）
den=$(ax 'wm density 2>/dev/null' | tr -dc '0-9 :a-zA-Z'); info "wm density：${den:-读不到}"
info "↑ 和该机型官方 dpi 核对（进度记录里标了 dpi 待真机核实）"

# ========== 3. 硬件缺的（和显卡一个性质，等板子）==========
sect "3. 硬件缺失（等板子，和显卡同类）"

# 显卡 / 渲染器
rend=$(ax 'dumpsys SurfaceFlinger 2>/dev/null | grep -iE "GLES|renderer" | head -1')
info "渲染器：${rend:-读不到}"
echo "$rend" | grep -qiE 'swiftshader|software|llvmpipe|android emulator' && brd "GPU 是软件渲染（SwiftShader）——最大破绽，等板子上真 Mali 解决" || info "渲染器未见软件渲染字样（到板子上再确认是硬件 Mali）"
ls_dri=$(ax 'ls /dev/dri 2>/dev/null'); [ -z "$ls_dri" ] && brd "没有 /dev/dri（没有 GPU 设备节点）" || info "/dev/dri：$ls_dri"

# 传感器
sens=$(ax 'dumpsys sensorservice 2>/dev/null | grep -ciE "accelerometer|gyroscope|proximity|light"')
[ "${sens:-0}" -ge 1 ] && info "传感器条目：$sens" || brd "没有重力/陀螺仪/光线/距离等传感器（真机都有）"

# 摄像头
cam=$(ax 'dumpsys media.camera 2>/dev/null | grep -ciE "Camera [0-9]|device id"')
[ "${cam:-0}" -ge 1 ] && info "摄像头条目：$cam" || brd "没有摄像头"

# 电话 / SIM / IMEI
simop=$(gp gsm.sim.operator.alpha); brd_sim=$(gp gsm.operator.alpha)
if [ -z "$simop$brd_sim" ]; then brd "没有 SIM / 运营商信息（真机即便无卡也有电话子系统）"; else info "SIM 运营商：$simop$brd_sim"; fi

# 蓝牙
ax 'command -v dumpsys >/dev/null && dumpsys bluetooth_manager 2>/dev/null | grep -qi "enabled\|address" && echo yes' | grep -q yes && info "有蓝牙子系统" || brd "没有蓝牙"

# 电池：容器里常年 AC 供电 / 固定电量
batt=$(ax 'dumpsys battery 2>/dev/null')
bl=$(echo "$batt" | grep -iE 'level|AC powered|status' | tr '\n' ' '); info "电池：${bl:-读不到}"
echo "$batt" | grep -qi 'AC powered: true' && brd "电池显示一直 AC 供电（真机用电池时为 false），容器典型特征"

# ========== 小结 ==========
sect "小结"
echo "  已遮住 $hide 项 · 现在可做 $nowfix 项 · 等板子 $board 项"
echo
echo "  · [现在可做] 的项，这台甲骨文机器上就能一项项改（内核系统调用那条除外，容器改不了）。"
echo "  · [等板子] 的项都是硬件缺的，和显卡一个性质，RK3588 板子到手插上真硬件后再处理，现在做了也白做。"
echo "  · 这个脚本只读不改，可以随时跑。"
