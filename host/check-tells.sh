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
#   [容器改不了] 实测在容器里没有干净办法（如 /proc/mounts 软链接遮不住、uname 系统调用），要拦系统调用或改镜像/容器运行方式
#   [等板子]  硬件缺的（显卡、摄像头等），和显卡一个性质，要等 RK3588 板子
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
hide=0; nowfix=0; board=0; cant=0
ax()  { docker exec "$C" sh -c "$1" 2>/dev/null; }                 # 在安卓容器里跑
gp()  { docker exec "$C" getprop "$1" 2>/dev/null; }
hid() { echo "  [已遮住]  $*"; hide=$((hide+1)); }
now() { echo "  [现在可做] $*"; nowfix=$((nowfix+1)); }
# [容器改不了]：实测在容器里没有干净办法，要靠拦系统调用（脆弱）或改镜像/容器运行方式，归到镜像/板子阶段
cnt() { echo "  [容器改不了] $*"; cant=$((cant+1)); }
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
sect "2. 系统 / 容器特征"

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
# 注意：真机各分区指纹本来就不一样（小米 / 联发科的 vendor、odm 指纹停在出厂安卓版本，system 是通用镜像），
# 有真机分区数据（机型档案 buildProps）的机型照搬真机，不一致是对的（踩坑 #52）；这里只列出来看。
info "vendor 指纹：${vendfp:-空}"; info "bootimage 指纹：${bootimg:-空}"
# 全分区扫描：各分区的 fingerprint / build.id / product.name 不能残留 redroid / test-keys / userdebug / eng.frank
# （system_ext / vendor_dlkm / odm_dlkm / system_dlkm 这些分区容易漏，魔云腾改机 SDK 也处理，2026-11-15 已覆盖）
partleak=0
for part in system vendor odm product system_ext vendor_dlkm odm_dlkm system_dlkm bootimage; do
  for attr in build.fingerprint build.id build.tags build.type; do
    v=$(gp "ro.${part}.${attr}")
    echo "$v" | grep -qiE 'redroid|test-keys|userdebug|eng\.frank|UD2A' && { now "分区属性 ro.${part}.${attr}=$v 残留默认镜像痕迹，fingerprintProps 应覆盖"; partleak=$((partleak+1)); }
  done
  n=$(gp "ro.product.${part}.name")
  echo "$n" | grep -qiE 'redroid' && { now "ro.product.${part}.name=$n 还是 redroid_arm64_only，应盖成真实产品名"; partleak=$((partleak+1)); }
done
[ "$partleak" = 0 ] && hid "全部 9 个分区的 fingerprint/build/product.name 都已盖干净（无 redroid/test-keys/userdebug/eng.frank 残留）"
# ro.build.description 含空格，启动参数传不了，是已知的镜像阶段残留
desc=$(gp ro.build.description); echo "$desc" | grep -qiE 'redroid|userdebug|test-keys' && brd "ro.build.description=$desc 残留（含空格，启动参数传不了，要镜像阶段改 build.prop；魔云腾 build.prop 剥离层一并处理）"

# 内核：App 直接调 uname() 拿到的是宿主机内核（fakefs 只改文件视图，改不了系统调用）
unamer=$(ax 'uname -r'); info "uname -r（系统调用）：$unamer"
cnt "uname() 系统调用返回宿主机内核 $unamer，fakefs 只改 /proc 文件、改不了这个；要拦系统调用（脆弱），真机上是对应机型内核"

# /proc/mounts：overlay、docker、fakefs 自己的 bind mount 都会露
mnt=$(ax 'cat /proc/mounts')
echo "$mnt" | grep -qi overlay && cnt "/proc/mounts 有 overlay + containerd 路径（容器特征）。实测遮不住：/proc/mounts 是软链接→/proc/self/mounts，bind 会跟链接绑到当前 pid 的 proc 上、对别的进程无效，procfs 又不让改符号链接（踩坑见记录）。要靠拦系统调用或换容器运行方式（镜像阶段）"
echo "$mnt" | grep -qiE '/dev/null .* (cpuinfo|midr|version|osrelease)' && info "fakefs 的 bind mount 挂载点本身也在 /proc/mounts 里（遮了内容、露了手法）——同上，遮不住"
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

# 网卡：有 WiFi 镜像时手机里只有 lo + wlan0（踩坑 #57），否则是容器的 eth0 / 10.88 内网
ifs=$(ax 'ip -o addr show 2>/dev/null || cat /proc/net/fib_trie 2>/dev/null | head')
if echo "$ifs" | grep -qE '10\.88\.|veth|eth0'; then
  now "网卡是容器虚拟网卡 eth0 / 10.88 内网段，安卓看到的是「以太网」（真机是 WiFi）。换成带 WiFi 的镜像（host/build-wifi-image.sh，agent.env 里指过去）后管家自动配虚拟 WiFi"
elif echo "$ifs" | grep -q wlan0; then
  wst=$(ax 'cmd wifi status 2>/dev/null | grep -m1 "connected to"')
  [ -n "$wst" ] && hid "手机走 WiFi（只有 wlan0，${wst#*Wifi is }），没有 eth0" || now "有 wlan0 但 WiFi 没连上（看管家日志里的 WiFi 行）"
fi

# 屏幕密度：和机型档案对不对得上（文档里标过待核实）
den=$(ax 'wm density 2>/dev/null' | tr -dc '0-9 :a-zA-Z'); info "wm density：${den:-读不到}"
info "↑ 和该机型官方 dpi 核对（进度记录里标了 dpi 待真机核实）"

# ========== 2b. 2026-11-16 ADB 体检修掉的项（防复发，踩坑 #44–#49）==========
sect "2b. 开机后处理（防复发）"
ax 'ls /.dockerenv' | grep -q dockerenv && now "/.dockerenv 还在（开机后 removeTraces 应删掉）" || hid "/.dockerenv 已删"
ax 'ls /pp-init' | grep -q pp-init && now "/pp-init 启动器挂载点没清掉" || true
ax 'pm list packages org.chromium.webview_shell' | grep -q webview_shell && now "AOSP 测试 App org.chromium.webview_shell 还在（开机设置应卸掉）" || hid "AOSP 测试 App webview_shell 已卸"
ch=$(gp ro.build.characteristics); [ "$ch" = default ] && now "ro.build.characteristics=default（模拟器默认值）" || hid "ro.build.characteristics=$ch"
for p in dalvik.vm.isa.arm64.variant ro.bionic.cpu_variant; do
  v=$(gp "$p"); [ "$v" = generic ] && now "$p=generic（通用编译，真机写具体核心）" || info "$p=${v:-空}"
done
fps=$(ax 'dumpsys SurfaceFlinger 2>/dev/null | grep -m1 refresh-rate' | grep -oE '[0-9]+\.[0-9]+'); info "屏幕刷新率：${fps:-读不到} Hz"
[ -n "$fps" ] && awk "BEGIN{exit !($fps < 50)}" && now "刷新率只有 $fps Hz（redroid_fps 超过 120 会变 15Hz，见踩坑 #44）"
parts=$(ax 'grep "CPU part" /proc/cpuinfo | sort -u | wc -l'); info "cpuinfo 里不同核心类型数：${parts:-?}"
[ "${parts:-0}" = 1 ] && info "↑ 只有一种核心：宿主机核少时 fitCPUs 应按比例保留大核（8 核机型应看到 2 种以上）"
cpid=$(docker inspect -f '{{.State.Pid}}' "$C")
if [ "$(readlink /proc/$cpid/ns/time 2>/dev/null)" != "$(readlink /proc/self/ns/time)" ]; then hid "安卓在自己的时间命名空间里（开机时长从 0 开始；注意 docker exec 读 /proc/uptime 仍是宿主机值，要用 adb shell 看）"
else now "安卓和宿主机共用时钟：uptime 显示宿主机开了多久（管家 -no-timens？内核不支持 CONFIG_TIME_NS？见踩坑 #49）"; fi
bpres=$(ax 'dumpsys battery 2>/dev/null' | grep -m1 'present:' | awk '{print $2}'); [ "$bpres" = false ] && now "电池 present=false（真机永远 true，开机设置应设 present 1）" || info "电池 present=${bpres:-?}"
adbon=$(ax 'settings get global adb_enabled'); [ "$adbon" = 1 ] && info "USB 调试开着（adb_enabled=1，App 能读到）：这台在管理页打开了 ADB；平时用完关掉" || hid "USB 调试关着（adb_enabled=${adbon:-?}）"
desc=$(gp ro.build.description); echo "$desc" | grep -qiE 'redroid|userdebug|test-keys' || hid "ro.build.description 已是真机格式：$desc"
pb=$(docker exec -u 10100 "$C" sh -c 'cat /product/etc/build.prop /system_ext/etc/build.prop' 2>/dev/null | grep -ciE 'redroid|userdebug|test-keys')
[ "${pb:-0}" -gt 0 ] && now "App 能直接读的 /product、/system_ext 的 build.prop 里还有 $pb 行 redroid / userdebug / test-keys（开机应按最终身份改写，踩坑 #55）" || hid "App 能读的 build.prop 文件里没有 redroid 痕迹"
bton=$(ax 'settings get global bluetooth_on'); [ "$bton" = 1 ] && now "bluetooth_on=1：没有蓝牙硬件，每次开机蓝牙进程会连崩（踩坑 #48）" || info "bluetooth_on=${bton:-?}"
crash=$(ax 'logcat -b crash -d 2>/dev/null' | grep -cE 'Fatal signal|FATAL EXCEPTION')
[ "${crash:-0}" -gt 0 ] && { now "这次开机有 $crash 次崩溃（下面是崩的进程）"; ax 'logcat -b crash -d 2>/dev/null' | grep -oE 'Process: [a-z0-9._:]+|pid [0-9]+ \([^)]*\)' | sort | uniq -c | sort -rn | head -5 | sed 's/^/             /'; } || hid "这次开机没有崩溃"

# ========== 3. 硬件缺的（和显卡一个性质，等板子）==========
sect "3. 硬件缺失（等板子，和显卡同类）"

# 显卡 / 渲染器
rend=$(ax 'dumpsys SurfaceFlinger 2>/dev/null | grep -iE "GLES|renderer" | head -1')
info "渲染器：${rend:-读不到}"
echo "$rend" | grep -qiE 'swiftshader|software|llvmpipe|android emulator' && brd "GPU 是软件渲染（SwiftShader）——最大破绽，等板子上真 Mali 解决" || info "渲染器未见软件渲染字样（到板子上再确认是硬件 Mali）"
ls_dri=$(ax 'ls /dev/dri 2>/dev/null'); [ -z "$ls_dri" ] && brd "没有 /dev/dri（没有 GPU 设备节点）" || info "/dev/dri：$ls_dri"

# 传感器（踩坑 #59：模拟器 HAL + 管家喂数据）
hw=$(ax 'dumpsys sensorservice 2>/dev/null | grep -oE "Total [0-9]+ h/w sensors" | grep -oE "[0-9]+"')
if [ "${hw:-0}" -ge 1 ]; then
  hid "有 $hw 个硬件传感器（加速度 / 陀螺仪 / 磁力计 / 距离 / 光线等）"
  ax 'dumpsys sensorservice 2>/dev/null' | grep -qiE "goldfish|Android Open Source Project" && now "传感器名字 / 厂商还带模拟器字样（镜像没用 patch-ranchu.py 重建）"
  ev=$(ax 'dumpsys sensorservice 2>/dev/null | grep -A2 "^Accelerometer: last" | tail -1')
  [ -n "$ev" ] && info "加速度最近一条：$ev" || now "加速度计没有数据（看手机里 vendor.sensord 进程在不在）"
  info "传感器名字、量程是通用值（各机型真实芯片名要真机数据）"
else
  now "没有传感器（换带传感器的镜像：host/build-wifi-image.sh 重建，标签 prism.sensors=1）"
fi

# 摄像头
cam=$(ax 'dumpsys media.camera 2>/dev/null | grep -ciE "Camera [0-9]|device id"')
[ "${cam:-0}" -ge 1 ] && info "摄像头条目：$cam" || brd "没有摄像头"

# 电话 / SIM / IMEI
simop=$(gp gsm.sim.operator.alpha); brd_sim=$(gp gsm.operator.alpha)
if [ -z "$simop$brd_sim" ]; then brd "没有 SIM / 运营商信息（真机即便无卡也有电话子系统）"; else info "SIM 运营商：$simop$brd_sim"; fi

# 蓝牙
ax 'command -v dumpsys >/dev/null && dumpsys bluetooth_manager 2>/dev/null | grep -qi "enabled\|address" && echo yes' | grep -q yes && info "有蓝牙子系统" || brd "没有蓝牙"

# 电池：已由管家的电池漂移处理（随机 60% 放电/40% 充电，插口+status 一致，后台慢漂）。
# 这里只做提示：显示 AC 本身不是破绽（真机充电时也是 AC）；真正的破绽是"永远 AC、电量不动"。
batt=$(ax 'dumpsys battery 2>/dev/null')
bl=$(echo "$batt" | grep -iE 'level|AC powered|status' | tr '\n' ' '); info "电池：${bl:-读不到}"
if echo "$batt" | grep -qi 'AC powered: true'; then
  info "↑ 这台当前是充电态（AC）。注：电池漂移已实现（battery.go），放电态会显示 false；若所有手机永远 AC 才是问题"
else
  hid "电池为放电态（AC=false），电池漂移已生效"
fi

# ========== 小结 ==========
sect "小结"
echo "  已遮住 $hide 项 · 现在可做 $nowfix 项 · 容器改不了 $cant 项 · 等板子 $board 项"
echo
echo "  · [现在可做] 的项，这台甲骨文机器上就能一项项改。"
echo "  · [容器改不了] 的项，实测在容器里没有干净办法（uname/mounts/网卡都试过），要靠拦系统调用（脆弱）或改镜像/容器运行方式，归镜像/板子阶段。"
echo "  · [等板子] 的项都是硬件缺的，和显卡一个性质，RK3588 板子到手插上真硬件后再处理，现在做了也白做。"
echo "  · 这个脚本只读不改，可以随时跑。"
