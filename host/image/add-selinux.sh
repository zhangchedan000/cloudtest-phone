#!/bin/sh
# 让镜像能在魔云腾 Q1 上开真 SELinux（踩坑 #135）：手机里 getenforce = Enforcing，和真机一样。
#
# 容器版安卓（redroid）为了能在没有 SELinux 的服务器上开机，改了几个地方，都要改回来：
#   1. /system/lib64/libselinux.so 对谁都说「SELinux 关了」、查标签一律返回假的 HACKED → 换成魔云腾 dobox 同版本的原版
#      （导出函数、依赖一模一样）；
#   2. libbinder.so（系统 + vendor）把内核给的「调用方 SELinux 身份」一律换成 HACKED，服务管家按它查规则会全拒 → 同上换原版；
#   3. init 起服务时，没写 seclabel 的服务一律用 HACKED 当身份（原版这里按程序文件的标签算该进哪个域，被整段删了）
#      → 把那一处的长度改成 0（空身份：exec 时由内核按文件标签自动切域，和原版结果一样）；
#      能改的服务配置（system / vendor / product / system_ext / odm 下的 .rc）全部补上 seclabel：
#      在加载了这台镜像规则的临时容器里，用内核算「init 执行这个程序文件会进哪个域」——和原版 init 算的一样；
#   4. Docker 镜像层里的文件没有 SELinux 标签（打包时丢了）→ 在加载了规则的临时容器里用 setfiles 按镜像自己的
#      file_contexts 给每一层打标签（必须在 SELinux 命名空间里打：宿主机直接写的标签手机那边不认）。
# 原镜像留成 <镜像>-orig-sel；镜像加标签 prism.selinux=1（管家 -selinux 只对它生效）；重复跑会跳过已经做好的。
#
# 用法（root，板子上）：sh host/image/add-selinux.sh <镜像> <安卓版本 14|15|16>
set -eu
img=$1; ver=$2
B=${PP_BUILD:-/mmc/pp-build}
log(){ echo "$(date +%T) [selinux] $*"; }
die(){ echo "[selinux] 错误：$*" >&2; exit 1; }
SEL_VER=20
# 给镜像每一层打标签：在加载了规则（$1/policy）的临时容器里，setfiles 按 $1/file_contexts 打 $2 这个镜像的所有层；
# $3 = top 只打最上面那层（增量补丁用：下面的层和别的镜像共用，不碰）
label_layers() {
  lw=$1; li=$2
  if [ "${3:-}" = top ]; then docker image inspect -f '{{.GraphDriver.Data.UpperDir}}' "$li" > "$lw/layers"
  else docker image inspect -f '{{.GraphDriver.Data.LowerDir}}:{{.GraphDriver.Data.UpperDir}}' "$li" | tr ':' '\n' | grep -v '^$' > "$lw/layers"; fi
  DR=$(docker info -f '{{.DockerRootDir}}')
  docker run --rm --privileged --platform linux/arm64 -v "$lw:/w" -v "$DR:$DR" -v "$B/kmod/aptcache:/aptc" debian:12-slim bash -ec '
    apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq --no-install-recommends -o Dir::Cache::Archives=/aptc policycoreutils python3 >/dev/null 2>&1
    mount -t selinuxfs selinuxfs /sys/fs/selinux
    python3 -c "
import os
d=open(\"/w/policy\",\"rb\").read(); fd=os.open(\"/sys/fs/selinux/load\",os.O_WRONLY); assert os.write(fd,d)==len(d); os.close(fd)"
    n=0
    while read -r L; do
      [ -d "$L" ] || continue
      setfiles -F -r "$L" /w/file_contexts "$L" 2>&1 | grep -v "^setfiles: Could not set context\|No such file" | tail -3 || true
      n=$((n + 1))
    done < /w/layers
    echo "打了 $n 层的标签"
  '
}
# 镜像里 /system/bin/init 有没有 SELinux 标签（读最上面那层的文件属性）
has_labels() {
  for L in $(docker image inspect -f '{{.GraphDriver.Data.UpperDir}}:{{.GraphDriver.Data.LowerDir}}' "$1" | tr ':' ' '); do
    [ -e "$L/system/bin/init" ] || continue
    python3 -c "import os,sys; os.getxattr(sys.argv[1], 'security.selinux')" "$L/system/bin/init" 2>/dev/null
    return $?
  done
  return 1
}
# 从镜像里取打标签要的规则和 file_contexts，放到 $2/policy、$2/file_contexts
label_inputs() {
  rcid=$(docker create "$1" /system/bin/sh)   # 不启动，只为拷文件；带个命令，没有启动命令的镜像也能建
  docker cp "$rcid:/vendor/etc/selinux/precompiled_sepolicy" "$2/policy" >/dev/null || { docker rm "$rcid" >/dev/null; die "镜像里没有 precompiled_sepolicy"; }
  : > "$2/file_contexts"
  for f in /system/etc/selinux/plat_file_contexts /system_ext/etc/selinux/system_ext_file_contexts \
           /product/etc/selinux/product_file_contexts /vendor/etc/selinux/vendor_file_contexts /odm/etc/selinux/odm_file_contexts; do
    docker cp "$rcid:$f" "$2/fc.part" >/dev/null 2>&1 && cat "$2/fc.part" >> "$2/file_contexts" || true
  done
  rm -f "$2/fc.part"; docker rm "$rcid" >/dev/null
}
# 增量补丁：做好的镜像上叠一层、只给这层打标签，不用原镜像、不用整套重做（导出包导进来的镜像也能补）。
# 瑞芯微硬件编解码服务的 seccomp 白名单（dobox 原样拿来的）没有 sysinfo：开机 / 几台一起看画面时编码库会调它 →
# 被 SIGSYS 杀（不留 tombstone）→ 系统服务拿不到编解码器整个框架崩（安卓 15/16 软重启还可能卡进崩溃循环、WiFi / ADB 全断）
# → 实时画面落到软件编码。调试模块挂 force_sig_seccomp 抓到全是 179 = sysinfo（2026-11-23，踩坑 #144）
C2P=/vendor/etc/seccomp_policy/android.hardware.media.c2@1.1-seccomp_policy
c2_seccomp_fix() {
  PW=$(mktemp -d "$B/c2fix.XXXX")
  pcid=$(docker create "$1" /system/bin/sh)
  if docker cp "$pcid:$C2P" "$PW/p" >/dev/null 2>&1 && ! grep -q '^sysinfo:' "$PW/p"; then
    docker rm "$pcid" >/dev/null
    mkdir -p "$PW/ctx$(dirname "$C2P")"
    { cat "$PW/p"; printf '\n# 瑞芯微编码库会调（被拦就 SIGSYS，踩坑 #144）\nsysinfo: 1\n'; } > "$PW/ctx$C2P"; chmod 644 "$PW/ctx$C2P"
    printf 'FROM %s\nCOPY ctx/ /\n' "$1" > "$PW/Dockerfile"
    docker build -q -t "$1" "$PW" >/dev/null
    label_inputs "$1" "$PW"
    label_layers "$PW" "$1" top
    up=$(docker image inspect -f '{{.GraphDriver.Data.UpperDir}}' "$1")
    python3 -c "import os,sys; print(os.getxattr(sys.argv[1], 'security.selinux').decode().strip(chr(0)))" "$up$C2P" >/dev/null 2>&1 || die "白名单补丁那层没打上标签"
    log "✓ $1：硬件编解码的 seccomp 白名单补了 sysinfo"
  else
    docker rm "$pcid" >/dev/null 2>&1 || true
  fi
  rm -rf "$PW"
}
# 增量补丁：Vulkan 没有显卡设备（踩坑 #148）。系统声称支持 Vulkan 1.1，App 枚举却是 0 个设备：/vendor/lib64/hw 里是 redroid 带的
# 开源驱动（radeon / nouveau / freedreno / panfrost / virtio / lavapipe……，本身也是痕迹），没有 Mali 的入口模块，ro.hardware.vulkan 也是空的。
# Mali 驱动 libGLES_mali.so 本身带 Vulkan，只缺 dobox 里那个 10K 的入口模块 vulkan.mali.so（依赖就是 libGLES_mali.so）。
# 补上入口模块（App 进程要加载：same_process_hal_file）、开机最早设 ro.hardware.vulkan=mali；只给新叠的这层打标签。开源驱动开机时删
VKM=/vendor/lib64/hw/vulkan.mali.so
vulkan_fix() {
  # 入口模块只依赖 libGLES_mali.so，和安卓版本无关；dobox Q15 自己就没带（只有开源的 pastel），用 16 / 14 的
  src=""
  for sv in $ver 16 14 15; do [ -f "$B/dobox$sv-vendor/lib64/hw/vulkan.mali.so" ] && { src=$B/dobox$sv-vendor/lib64/hw/vulkan.mali.so; break; }; done
  VW=$(mktemp -d "$B/vkfix.XXXX")
  vcid=$(docker create "$1" /system/bin/sh)
  if docker cp "$vcid:$VKM" "$VW/has" >/dev/null 2>&1; then docker rm "$vcid" >/dev/null; rm -rf "$VW"; return 0; fi
  if [ -z "$src" ]; then docker rm "$vcid" >/dev/null; rm -rf "$VW"; log "· $1：没有 dobox 的 vulkan.mali.so（$B/dobox*-vendor/lib64/hw），Vulkan 先不补"; return 0; fi
  mkdir -p "$VW/ctx/vendor/lib64/hw" "$VW/ctx/vendor/etc/init" "$VW/ctx/vendor/etc/selinux"
  cp "$src" "$VW/ctx$VKM"; chmod 644 "$VW/ctx$VKM"
  printf 'on early-init\n    setprop ro.hardware.vulkan mali\n' > "$VW/ctx/vendor/etc/init/vulkan-mali.rc"; chmod 644 "$VW/ctx/vendor/etc/init/vulkan-mali.rc"
  docker cp "$vcid:/vendor/etc/selinux/vendor_file_contexts" "$VW/ctx/vendor/etc/selinux/vendor_file_contexts" >/dev/null
  grep -q 'vulkan\\.mali' "$VW/ctx/vendor/etc/selinux/vendor_file_contexts" ||
    printf '/vendor/lib64/hw/vulkan\\.mali\\.so u:object_r:same_process_hal_file:s0\n' >> "$VW/ctx/vendor/etc/selinux/vendor_file_contexts"
  chmod 644 "$VW/ctx/vendor/etc/selinux/vendor_file_contexts"
  # redroid 带的开源 Vulkan 驱动（radeon / nouveau / freedreno……）在镜像里删不掉（构建时跑不了安卓的 rm），
  # 由手机里的小助手每次开机最早删（容器每次开机是新的一层，等于没有），见 agent/internal/phonecmd/label.go
  docker rm "$vcid" >/dev/null
  printf 'FROM %s\nCOPY ctx/ /\n' "$1" > "$VW/Dockerfile"
  docker build -q -t "$1" "$VW" >/dev/null || { rm -rf "$VW"; die "Vulkan 补丁那层没建成（$1）"; }
  label_inputs "$1" "$VW"
  label_layers "$VW" "$1" top
  up=$(docker image inspect -f '{{.GraphDriver.Data.UpperDir}}' "$1")
  ctx=$(python3 -c "import os,sys; print(os.getxattr(sys.argv[1], 'security.selinux').decode().strip(chr(0)))" "$up$VKM" 2>/dev/null)
  [ "$ctx" = "u:object_r:same_process_hal_file:s0" ] || die "Vulkan 入口模块标签不对：$ctx"
  log "✓ $1：Vulkan 补上 Mali 入口模块（ro.hardware.vulkan=mali，来自 $(basename $(dirname $(dirname $(dirname $src)))))"
  rm -rf "$VW"
}
# 增量补丁：摄像头（踩坑 #152）。模拟器的模拟摄像头服务，文件由 add-camera.sh 准备；镜像加标签 prism.camera=1（管家据此按机型换摄像头参数）
CAMB=/vendor/bin/hw/android.hardware.camera.provider@2.7-service
camera_fix() {
  CW=$(mktemp -d "$B/camfix.XXXX")
  crc=0; sh "$(cd "$(dirname "$0")" && pwd)/add-camera.sh" "$1" "$ver" "$CW/w" || crc=$?
  if [ "$crc" = 2 ]; then rm -rf "$CW"; return 0; fi
  if [ "$crc" != 0 ]; then rm -rf "$CW"; log "· $1：摄像头没补上（见上面的原因），先不补"; return 0; fi
  printf 'FROM %s\nCOPY ctx/ /\nLABEL prism.camera=1\n' "$1" > "$CW/w/Dockerfile"
  docker build -q -t "$1" "$CW/w" >/dev/null || { rm -rf "$CW"; die "摄像头那层没建成（$1）"; }
  label_inputs "$1" "$CW/w"
  label_layers "$CW/w" "$1" top
  up=$(docker image inspect -f '{{.GraphDriver.Data.UpperDir}}' "$1")
  ctx=$(python3 -c "import os,sys; print(os.getxattr(sys.argv[1], 'security.selinux').decode().strip(chr(0)))" "$up$CAMB" 2>/dev/null)
  [ "$ctx" = "u:object_r:hal_camera_default_exec:s0" ] || die "摄像头服务标签不对：$ctx"
  log "✓ $1：补上摄像头服务"
  rm -rf "$CW"
}
cur=$(docker image inspect -f '{{index .Config.Labels "prism.selinux"}}' "$img" 2>/dev/null || true)
if [ "$cur" = "$SEL_VER" ]; then
  if has_labels "$img"; then c2_seccomp_fix "$img"; vulkan_fix "$img"; camera_fix "$img"; log "$img 已经做好（第 $SEL_VER 版），跳过"; exit 0; fi
  # docker save / load（导出包换新板）只带 security.capability，不带 security.selinux：镜像改过的内容都在，标签全丢了，
  # 手机开强制模式会全被拦。按镜像自己的规则和 file_contexts 把每一层重打一遍（2026-11-23 查实）
  log "$img 是做好的（第 $SEL_VER 版），但导入时丢了文件标签：重打"
  RW=$(mktemp -d "$B/selrel.XXXX")
  label_inputs "$img" "$RW"
  label_layers "$RW" "$img"
  rm -rf "$RW"
  has_labels "$img" || die "重打以后还是没有标签"
  log "✓ $img：标签重打好了"
  c2_seccomp_fix "$img"
  vulkan_fix "$img"
  camera_fix "$img"
  exit 0
fi
base=$img
if [ -n "$cur" ] && [ "$cur" != "<no value>" ]; then
  docker image inspect "$img-orig-sel" >/dev/null 2>&1 || die "$img 是旧版（$cur），但找不到原镜像 $img-orig-sel"
  base=$img-orig-sel
fi
command -v debugfs >/dev/null || die "缺 debugfs（apk add e2fsprogs-extra）"

# ---- 1) 魔云腾 dobox 同版本的原版库 ----
DS=$B/dobox$ver-system DV=$B/dobox$ver-vendor
if [ ! -f "$DS/lib64/libbinder.so" ]; then
  dob=$(docker images --format '{{.Repository}}:{{.Tag}}' | grep "mytos/dobox:Q$ver" | head -1)
  [ -n "$dob" ] || die "没有魔云腾 dobox Q$ver 镜像"
  log "从 $dob 取 system/lib64"
  LP=$B/tools/lpunpack.py
  [ -f "$LP" ] || die "缺 $LP（先跑一次 host/image/add-hwcodec.sh，它会下载）"
  W=$(mktemp -d "$B/dsys.XXXX")
  cid=$(docker create "$dob"); docker cp "$cid:/super.img" "$W/super.img" >/dev/null; docker rm "$cid" >/dev/null
  python3 -I "$LP" -p system "$W/super.img" "$W" >/dev/null
  mkdir -p "$DS"; debugfs -R "rdump /system/lib64 $DS" "$W/system.img" >/dev/null 2>&1 || true
  rm -rf "$W"
  [ -f "$DS/lib64/libbinder.so" ] || die "dobox Q$ver 的 system 里没有 libbinder.so"
fi

W=$(mktemp -d "$B/selimg.XXXX"); trap 'rm -rf "$W"' EXIT
O=$W/overlay; mkdir -p "$O" "$W/x"
cid=$(docker create "$base")
docker cp "$cid:/system/bin/init" "$W/x/init" >/dev/null
docker cp "$cid:/vendor/etc/selinux/precompiled_sepolicy" "$W/x/policy" >/dev/null || die "镜像里没有 precompiled_sepolicy"
# 各分区的 file_contexts 分开存（fc.1 ~ fc.5，安卓读的顺序），后面改了 vendor 那份再按顺序拼
i=0
for f in /system/etc/selinux/plat_file_contexts /system_ext/etc/selinux/system_ext_file_contexts \
         /product/etc/selinux/product_file_contexts /vendor/etc/selinux/vendor_file_contexts /odm/etc/selinux/odm_file_contexts; do
  i=$((i + 1)); : > "$W/x/fc.$i"
  docker cp "$cid:$f" "$W/x/fc.$i" >/dev/null 2>&1 || true
done
cat "$W/x"/fc.[1-5] > "$W/x/file_contexts"
for d in system/etc/init system/etc/init/hw vendor/etc/init vendor/etc/init/hw odm/etc/init product/etc/init system_ext/etc/init; do
  mkdir -p "$W/rc/$d"; docker cp "$cid:/$d/." "$W/rc/$d/" >/dev/null 2>&1 || true
done
for f in system/lib64/libselinux.so system/lib64/libbinder.so vendor/lib64/libbinder.so; do
  mkdir -p "$W/x/$(dirname $f)"; docker cp "$cid:/$f" "$W/x/$f" >/dev/null 2>&1 || true
done
# 根目录下 system_ext / product / odm 多半是指向 /system/... 的链接：往链接里拷文件 docker build 会失败，记下真实位置
for d in system_ext product odm; do
  docker cp "$cid:/$d" "$W/x/lnk_$d" >/dev/null 2>&1 || true
done
docker cp "$cid:/vendor/etc/selinux/vendor_file_contexts" "$W/x/vendor_file_contexts" >/dev/null
: > "$W/x/vendor_seapp_contexts"; docker cp "$cid:/vendor/etc/selinux/vendor_seapp_contexts" "$W/x/vendor_seapp_contexts" >/dev/null 2>&1 || true
docker cp "$cid:/vendor/lib64" "$W/x/vlib64" >/dev/null
docker cp "$cid:/vendor/bin/hw/android.hardware.media.c2@1.1-service" "$W/x/has_c2" >/dev/null 2>&1 || true
docker rm "$cid" >/dev/null

# ---- 1b) RK3588 的设备节点补标签（显卡 mali、视频编解码 mpp / rga；Docker 的 /dev/loopN（真机在 /dev/block/ 下）：镜像是通用的 vendor，规则里没写，
#          落到通用的 device 类型，App 画界面 / 编码器会被拦）。和真 RK 手机用的类型一样；已经有的不重复加 ----
mkdir -p "$O/vendor/etc/selinux"
cp "$W/x/vendor_file_contexts" "$O/vendor/etc/selinux/vendor_file_contexts"
for kv in "/dev/mali0 u:object_r:gpu_device:s0" "/dev/mpp_service u:object_r:video_device:s0" "/dev/rga u:object_r:video_device:s0" \
          "/dev/dri(/.*)? u:object_r:gpu_device:s0" "/dev/loop[0-9]+ u:object_r:loop_device:s0" \
          "/system/apex/com\.android\.vndk\.[^/]+/lib(64)?(/.*)? u:object_r:system_lib_file:s0" \
          "/vendor/bin/hw/android\.hardware\.media\.c2@1\.1-service u:object_r:mediacodec_exec:s0" \
          "/vendor/bin/hw/android\.hardware\.gatekeeper@1\.0-service\.software u:object_r:hal_gatekeeper_default_exec:s0" \
          "/vendor/bin/hw/android\.hardware\.thermal@2\.0-service\.mock u:object_r:hal_thermal_default_exec:s0"; do
  p=${kv%% *}
  # redroid 自己加的 HAL 程序规则里没写类型：落到 vendor_file，init 起它不切域、以 init 身份跑，往 hwservicemanager
  # 注册被拒（锁屏密码 gatekeeper、温度 thermal），安卓一直等它开不了机。按真机同名 HAL 的类型标；规则里没这个类型就不加
  # 安卓 14 的 VNDK 是展开的目录 /system/apex/com.android.vndk.current（运行时挂成 .v34），规则里只写了不带版本号的，
  # 库落到 system_file，vendor 程序（boringssl 自检等）读不了 → 开机自检失败重启
  t=${kv##*:object_r:}; t=${t%%:*}
  grep -qa "$t" "$W/x/policy" || continue
  # 硬件编解码服务（host/image/add-hwcodec.sh 从 dobox 移植的）不在就不加。只管这一条：以前写成 /vendor/bin/hw/* 全算，
  # 没加硬件编码的镜像连温度 / 锁屏密码两条也跳过了；它和正式镜像共用底层，打标签时把共用层改回 vendor_file，
  # 正式镜像的这两个 HAL 起不来、安卓开不了机（2026-11-23 实测，踩坑 #144）
  case $p in *media\\.c2*) [ -f "$W/x/has_c2" ] || continue ;; esac
  grep -q "^$p[[:space:]]" "$W/x/file_contexts" || { echo "$kv" >> "$O/vendor/etc/selinux/vendor_file_contexts"; echo "$kv" >> "$W/x/file_contexts"; log "设备标签：$kv"; }
done
# 重新签过名的镜像（resign）：网络组件、蓝牙、媒体存储这几个原来各有专用签名（seinfo network_stack / bluetooth / media），
# 现在是平台签名，按 seapp_contexts 找不到身份：网络组件、蓝牙起不来（zygote 设身份失败），媒体存储落到 platform_app。
# 补上「平台签名的这几个」对应的行（没重新签名的镜像里它们不是平台签名，这几行不会命中）
cp "$W/x/vendor_seapp_contexts" "$O/vendor/etc/selinux/vendor_seapp_contexts"
cat >> "$O/vendor/etc/selinux/vendor_seapp_contexts" <<'SEAPP'
user=network_stack seinfo=platform domain=network_stack type=radio_data_file
user=bluetooth seinfo=platform domain=bluetooth type=bluetooth_data_file
user=_app seinfo=platform isPrivApp=true name=com.android.providers.media.module domain=mediaprovider_app type=privapp_data_file levelFrom=all
user=_app seinfo=platform isPrivApp=true name=com.android.providers.media.module:* domain=mediaprovider_app type=privapp_data_file levelFrom=all
SEAPP
chmod 644 "$O/vendor/etc/selinux/vendor_seapp_contexts"
# App 进程里直接加载的显卡库（gralloc mapper、EGL / Vulkan 驱动）和它们依赖的 vendor 库要是 same_process_hal_file
# （通用 vendor 规则里没写 Mali 的，落到 vendor_file，开强制模式 App 画不出界面）：按 ELF 依赖自动算
python3 - "$W/x/vlib64" "$W/x/file_contexts" >> "$O/vendor/etc/selinux/vendor_file_contexts" <<'EOF'
import os, re, struct, sys
lib, fc = sys.argv[1:3]
def needed(p):
    try: b = open(p, "rb").read()
    except OSError: return []
    if b[:4] != b"\x7fELF" or b[4] != 2: return []
    shoff, = struct.unpack_from("<Q", b, 0x28); shentsize, shnum = struct.unpack_from("<HH", b, 0x3a)
    secs = [struct.unpack_from("<IIQQQQIIQQ", b, shoff + i * shentsize) for i in range(shnum)]
    dyn = [x for x in secs if x[1] == 6]
    if not dyn: return []
    d = dyn[0]; st = secs[d[6]]; out = []
    for i in range(0, d[5], 16):
        tag, val = struct.unpack_from("<qQ", b, d[4] + i)
        if tag == 0: break
        if tag == 1:
            o = st[4] + val; out.append(b[o:b.index(b"\0", o)].decode())
    return out
start = []
for sub in ("egl", "hw"):
    d = os.path.join(lib, sub)
    for n in os.listdir(d) if os.path.isdir(d) else []:
        if sub == "egl" or re.match(r"(android\.hardware\.graphics\.mapper@.*|vulkan\..*)\.so$", n):
            start.append(os.path.join(sub, n))
seen, queue = set(), list(start)
while queue:
    rel = queue.pop()
    if rel in seen: continue
    seen.add(rel)
    for n in needed(os.path.join(lib, rel)):
        if os.path.exists(os.path.join(lib, n)): queue.append(n)
have = open(fc).read()
for rel in sorted(seen):
    path = "/vendor/lib64/" + rel
    pat = re.escape(path).replace("\\/", "/")
    if pat in have: continue
    print(f"{pat} u:object_r:same_process_hal_file:s0")
EOF
n=$(grep -c same_process_hal_file "$O/vendor/etc/selinux/vendor_file_contexts")
# 打标签用的合并 file_contexts 也换成新的 vendor_file_contexts（按安卓的顺序重新拼）
cp "$O/vendor/etc/selinux/vendor_file_contexts" "$W/x/fc.4"
cat "$W/x"/fc.[1-5] > "$W/x/file_contexts"
log "同进程显卡库：vendor_file_contexts 里 same_process_hal_file 共 $n 条"
chmod 644 "$O/vendor/etc/selinux/vendor_file_contexts"

# ---- 2) 换掉被改过的库（带 HACKED 的；或者像安卓 14 的 libselinux 那样，setfscreatecon 被改成直接返回 0） ----
stubbed() { # $1 = .so：导出函数 $2 的头两条指令是 mov w0, wzr; ret
  python3 - "$1" "$2" <<'PYEOF'
import struct, sys
b = open(sys.argv[1], "rb").read()
if b[:4] != b"\x7fELF" or b[4] != 2: sys.exit(1)
shoff, = struct.unpack_from("<Q", b, 0x28); shentsize, shnum = struct.unpack_from("<HH", b, 0x3a)
sh = [struct.unpack_from("<IIQQQQIIQQ", b, shoff + i * shentsize) for i in range(shnum)]
dyn = [x for x in sh if x[1] == 11]               # SHT_DYNSYM
if not dyn: sys.exit(1)
d = dyn[0]; strtab = sh[d[6]]
for i in range(d[5] // 24):
    name, info, other, shndx, value, size = struct.unpack_from("<IBBHQQ", b, d[4] + i * 24)
    n = b[strtab[4] + name:b.index(b"\0", strtab[4] + name)].decode()
    if n == sys.argv[2] and value:
        off = next(x[4] + value - x[3] for x in sh if x[1] == 1 and x[3] <= value < x[3] + x[5])
        sys.exit(0 if struct.unpack_from("<II", b, off) == (0x2a1f03e0, 0xd65f03c0) else 1)
sys.exit(1)
PYEOF
}
for f in system/lib64/libselinux.so system/lib64/libbinder.so vendor/lib64/libbinder.so; do
  [ -f "$W/x/$f" ] || continue
  grep -q HACKED "$W/x/$f" || { case $f in *libselinux.so) stubbed "$W/x/$f" setfscreatecon || continue ;; *) continue ;; esac; }
  case $f in system/*) src=$DS/lib64/$(basename $f) ;; vendor/*) src=$DV/lib64/$(basename $f) ;; esac
  [ -f "$src" ] || die "要换 /$f，但 dobox Q$ver 里没有 $src"
  grep -q HACKED "$src" && die "$src 也是改过的"
  case $f in *libselinux.so) stubbed "$src" setfscreatecon && die "$src 的 setfscreatecon 也是空的" ;; esac
  mkdir -p "$O/$(dirname $f)"; cp "$src" "$O/$f"; chmod 644 "$O/$f"
  log "换原版 /$f"
done

# ---- 3) init：没写 seclabel 的服务不再用 HACKED（长度 6 → 0） ----
python3 - "$W/x/init" "$O/system/bin/init" <<'EOF'
import os, struct, sys
src, dst = sys.argv[1:3]
b = bytearray(open(src, "rb").read())
MOV_W9_HA = 0x52882909            # mov w9, #0x4148 ("HA")
MOVK_W9_CK = 0x72a96869           # movk w9, #0x4b43, lsl #16 ("CK")
hits = []
for i in range(4, len(b) - 8, 4):
    if struct.unpack_from("<I", b, i)[0] != MOV_W9_HA or struct.unpack_from("<I", b, i + 4)[0] != MOVK_W9_CK:
        continue
    prev = struct.unpack_from("<I", b, i - 4)[0]
    if prev & 0xffffffe0 == 0x52800180:   # mov wN, #0xc（libc++ 短字符串长度字节 6<<1）
        hits.append((i - 4, prev))
def save(msg):
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    open(dst, "wb").write(b); os.chmod(dst, 0o755)
    print(msg)
if len(hits) == 1:
    off, prev = hits[0]
    struct.pack_into("<I", b, off, (prev & ~(0xffff << 5)) | (0 << 5))   # mov wN, #0
    save(f"init：0x{off:x} 处 HACKED 长度改成 0")
    sys.exit(0)
# 安卓 14 的 redroid 写法不一样："HACKED" 是只读区里的一个字符串常量（Service::Start 里取地址构造 std::string），
# 整个文件只出现一次：把它改成空串（首字节写 0），效果和上面一样
lit = b"\0HACKED\0"
if not hits and b.count(lit) == 1 and b.count(b"HACKED") == 1:
    off = b.find(lit) + 1
    b[off] = 0
    save(f"init：0x{off:x} 处字符串 HACKED 改成空串")
    sys.exit(0)
if b.find(b"incorrect label") >= 0:
    print("init 是原版（没有 HACKED 补丁），不用改"); sys.exit(0)
sys.exit(f"init 里找到 {len(hits)} 处 HACKED 补丁（应为 1），不敢改")
EOF

# ---- 3b) 规则补丁（host/image/selinux-rules.txt，sepatch 用官方 libsepol 加进二进制规则） ----
here=$(cd "$(dirname "$0")" && pwd)
RULES=${PP_SEL_RULES:-$here/selinux-rules.txt}
SEPATCH_C=${PP_SEPATCH_C:-$here/sepatch.c}
[ -f "$RULES" ] && [ -f "$SEPATCH_C" ] || die "缺 $RULES 或 $SEPATCH_C（和本脚本放一起）"
cp "$RULES" "$W/x/rules.txt"; cp "$SEPATCH_C" "$W/x/sepatch.c"
docker run --rm --platform linux/arm64 -v "$W:/w" -v "$B/kmod/aptcache:/aptc" debian:12-slim bash -ec '
  apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq --no-install-recommends -o Dir::Cache::Archives=/aptc gcc libc6-dev libsepol-dev >/dev/null 2>&1
  gcc -O2 -o /tmp/sepatch /w/x/sepatch.c -l:libsepol.a
  /tmp/sepatch /w/x/policy /w/x/policy.patched /w/x/rules.txt
'
mkdir -p "$O/vendor/etc/selinux"
cp "$W/x/policy.patched" "$O/vendor/etc/selinux/precompiled_sepolicy"; chmod 644 "$O/vendor/etc/selinux/precompiled_sepolicy"
cp "$W/x/policy.patched" "$W/x/policy"   # 后面算 seclabel、打标签都用补过的

# ---- 4) 服务补 seclabel（在加载了这台镜像规则的临时容器里让内核算） ----
docker run --rm --privileged --platform linux/arm64 -v "$W:/w" -v "$B/kmod/aptcache:/aptc" debian:12-slim bash -ec '
  apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq --no-install-recommends -o Dir::Cache::Archives=/aptc python3 python3-selinux >/dev/null 2>&1
  mount -t selinuxfs selinuxfs /sys/fs/selinux
  python3 - <<EOF
import os, re, selinux
data = open("/w/x/policy", "rb").read()
fd = os.open("/sys/fs/selinux/load", os.O_WRONLY); assert os.write(fd, data) == len(data); os.close(fd)
selinux.matchpathcon_init("/w/x/file_contexts")
cls = selinux.string_to_security_class("process")
INIT = "u:r:init:s0"
added = skipped = 0
for root, _, files in os.walk("/w/rc"):
    for fn in files:
        if not fn.endswith(".rc"): continue
        p = os.path.join(root, fn); rel = p[len("/w/rc"):]
        lines = open(p, errors="surrogateescape").read().split("\n")
        out, changed, i = [], False, 0
        while i < len(lines):
            l = lines[i]; out.append(l)
            m = re.match(r"^service\s+(\S+)\s+(\S+)", l)
            if m:
                # 服务那一行用 \ 续行时（比如 vold 的启动参数分好几行），seclabel 要加在续行结束之后，不然把参数截断了
                while lines[i].rstrip().endswith(chr(92)) and i + 1 < len(lines):
                    i += 1; out.append(lines[i])
                j = i + 1; has = False
                while j < len(lines) and not re.match(r"^(service|on|import)\s", lines[j]):
                    if re.match(r"^\s+seclabel\s", lines[j]): has = True
                    j += 1
                if not has:
                    exe = m.group(2)
                    try:
                        _, fcon = selinux.matchpathcon(exe, 0o100755)
                        _, dom = selinux.security_compute_create(INIT, fcon, cls)
                    except OSError as e:
                        dom = None; print(f"  ? {m.group(1)} {exe}: {e}")
                    if dom and dom != INIT:
                        out.append(f"    seclabel {dom}"); changed = True; added += 1
                    else:
                        skipped += 1; print(f"  init 身份：{m.group(1)} {exe}")
            i += 1
        if changed:
            dst = "/w/overlay" + rel; os.makedirs(os.path.dirname(dst), exist_ok=True)
            open(dst, "w", errors="surrogateescape").write("\n".join(out)); os.chmod(dst, 0o644)
print(f"服务补 seclabel：{added} 个；算出来还是 init 的（照原样）：{skipped} 个")
EOF
'

# 改过的 rc 挪到链接指向的真实目录
for d in system_ext product odm; do
  [ -L "$W/x/lnk_$d" ] && [ -d "$O/$d" ] || continue
  t=$(readlink "$W/x/lnk_$d" | sed 's#^/##')
  mkdir -p "$O/$t"; cp -a "$O/$d/." "$O/$t/"; rm -rf "$O/$d"
  log "rc：/$d → /$t"
done

# ---- 5) 出新镜像 ----
docker image inspect "$img-orig-sel" >/dev/null 2>&1 || docker tag "$img" "$img-orig-sel"
printf 'FROM %s\nCOPY overlay/ /\n' "$base" > "$W/Dockerfile"
docker build -q -t "$img-sel-wip" "$W" >/dev/null

# ---- 6) 给镜像每一层打标签（在加载了规则的临时容器里，setfiles 按镜像自己的 file_contexts） ----
label_layers "$W/x" "$img-sel-wip"
printf 'FROM %s\nLABEL prism.selinux=%s\n' "$img-sel-wip" "$SEL_VER" | docker build -q -t "$img" - >/dev/null
docker rmi "$img-sel-wip" >/dev/null 2>&1 || true
log "✓ $img：已能开 SELinux（原镜像留在 $img-orig-sel）"
c2_seccomp_fix "$img"
vulkan_fix "$img"
camera_fix "$img"
