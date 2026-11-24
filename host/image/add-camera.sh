#!/bin/sh
# 摄像头（踩坑 #152）：手机里一个摄像头都没有（dumpsys media.camera 0 个），系统特性却说有，App 一开相机就露馅。
# 从谷歌安卓模拟器（和 WiFi / 传感器 / 电话同一个 sys-img 包）拿它的「模拟摄像头」服务：camera.provider（AIDL）+
# libgooglecamerahal / libgooglecamerahwl_impl。它按 /vendor/etc/config/emu_camera_{back,front}.json 里的参数（传感器尺寸、
# 焦距、光圈、分辨率……）建摄像头，自己合成画面（不连模拟器），管家开机前按机型的真机参数换掉这两个文件（agent camera.go）。
# 服务程序改名成 android.hardware.camera.provider@2.7-service（镜像的 SELinux 规则按这个名字给 hal_camera_default 域，
# 名字里也不再带 google），服务名 vendor.camera-provider-2-7。依赖库镜像里没有的，从模拟器 vendor 拷。
#
# 这里只准备文件：sh add-camera.sh <镜像> <安卓版本> <输出目录>，输出目录下 ctx/ 就是要叠的一层（add-selinux.sh 的 camera_fix 叠层、打标签）。
# 已经有摄像头服务的镜像，退出码 2。
set -eu
img=$1; ver=$2; out=$3
B=${PP_BUILD:-/mmc/pp-build}
log(){ echo "$(date +%T) [camera] $*"; }
die(){ echo "[camera] 错误：$*" >&2; exit 1; }
CAMB=/vendor/bin/hw/android.hardware.camera.provider@2.7-service
case $ver in 14) emu=34_r04 ;; 15) emu=35_r02 ;; 16) emu=36_r02 ;; *) die "不支持安卓 $ver" ;; esac
zip=$B/cache/arm64-v8a-$emu.zip
[ -s "$zip" ] || die "没有模拟器镜像 $zip（先跑 host/build-wifi-image.sh，它会下载）"
command -v debugfs >/dev/null || die "缺 debugfs"

cid=$(docker create "$img" /system/bin/sh)
trap 'docker rm "$cid" >/dev/null 2>&1 || true' EXIT
mkdir -p "$out.probe"
if docker cp "$cid:$CAMB" "$out.probe/x" >/dev/null 2>&1; then rm -rf "$out.probe"; exit 2; fi
rm -rf "$out.probe"

E=$B/emu$emu
if [ ! -s "$E/vendor.ext4" ]; then
  log "解出模拟器 $emu 的 vendor"
  mkdir -p "$E"
  unzip -q -o -j "$zip" '*/vendor.img' -d "$E"
  python3 - "$E" <<'EOF'
import os, struct, sys
d = sys.argv[1]; f = open(os.path.join(d, 'vendor.img'), 'rb'); b = f.read(4096)
if b[0x438:0x43a] == b'\x53\xef':
    os.rename(os.path.join(d, 'vendor.img'), os.path.join(d, 'vendor.ext4')); sys.exit(0)
if b[0x200:0x208] == b'EFI PART':
    lba = struct.unpack_from('<Q', b, 0x200 + 0x48)[0]; f.seek(lba * 512); start, end = struct.unpack_from('<QQ', f.read(128), 32)
else:
    start, n = struct.unpack_from('<II', b, 0x1be + 8); end = start + n - 1
f.seek(start * 512); o = open(os.path.join(d, 'vendor.ext4'), 'wb'); left = (end - start + 1) * 512
while left > 0:
    c = f.read(min(left, 1 << 22))
    if not c: break
    o.write(c); left -= len(c)
o.close(); os.remove(os.path.join(d, 'vendor.img'))
EOF
fi
V=$E/vendor.ext4
dump(){ debugfs -R "dump $1 $2" "$V" >/dev/null 2>&1; [ -s "$2" ]; }

rm -rf "$out"; C=$out/ctx; mkdir -p "$C/vendor/bin/hw" "$C/vendor/lib64" "$C/vendor/etc/init" "$C/vendor/etc/vintf/manifest" "$C/vendor/etc/config"
dump /bin/hw/android.hardware.camera.provider@2.7-service-google "$C$CAMB" || die "模拟器 $emu 里没有 camera.provider@2.7-service-google"
chmod 755 "$C$CAMB"
# 真正出画面的库是运行时 dlopen 的（不在 NEEDED 里），单独拷，依赖下面一起找
dump /lib64/libgooglecamerahwl_impl.so "$C/vendor/lib64/libgooglecamerahwl_impl.so" || die "模拟器里没有 libgooglecamerahwl_impl.so"
chmod 644 "$C/vendor/lib64/libgooglecamerahwl_impl.so"
for f in emu_camera_back.json emu_camera_front.json; do dump /etc/config/$f "$C/vendor/etc/config/$f" || die "模拟器里没有 $f"; done
dump /etc/vintf/manifest/android.hardware.camera.provider@2.7-service-google.xml "$C/vendor/etc/vintf/manifest/android.hardware.camera.provider@2.7-service.xml" \
  || die "模拟器里没有摄像头服务的清单"
cat > "$C/vendor/etc/init/android.hardware.camera.provider@2.7-service.rc" <<EOF
service vendor.camera-provider-2-7 $CAMB
    class hal
    user system
    group system
    capabilities SYS_NICE
    rlimit rtprio 10 10
    task_profiles CameraServiceCapacity CameraServicePerformance
EOF

# 依赖：按 NEEDED 一层层找，镜像里（vendor、VNDK apex、系统的 LLNDK）没有的从模拟器 vendor/lib64 拷
python3 - "$C" "$V" "$cid" "$ver" "$out" <<'EOF'
import os, subprocess, sys
C, V, cid, ver, out = sys.argv[1:6]
probe = os.path.join(out, "probe")
LLNDK = {"libc.so", "libm.so", "libdl.so", "liblog.so", "libbinder_ndk.so", "libsync.so", "libvndksupport.so", "libnativewindow.so",
         "libandroid_net.so", "libmediandk.so", "libvulkan.so", "libEGL.so", "libGLESv1_CM.so", "libGLESv2.so", "libGLESv3.so", "libselinux.so"}
def needed(p):
    out = subprocess.run(["readelf", "-d", p], capture_output=True, text=True).stdout
    return [l.split("[")[1].split("]")[0] for l in out.splitlines() if "(NEEDED)" in l]
def in_image(lib):
    # 安卓 14 的 VNDK 在镜像里是摊开的目录 /system/apex/com.android.vndk.current（开机挂成 /apex/com.android.vndk.v34）
    for d in ("/vendor/lib64", "/system/apex/com.android.vndk.current/lib64", f"/system/apex/com.android.vndk.v{int(ver) + 20}/lib64"):
        if subprocess.run(["docker", "cp", f"{cid}:{d}/{lib}", probe], capture_output=True).returncode == 0:
            os.remove(probe)
            return True
    return False
todo = [os.path.join(C, "vendor/bin/hw/android.hardware.camera.provider@2.7-service"), os.path.join(C, "vendor/lib64/libgooglecamerahwl_impl.so")]
seen, copied, missing = set(), [], []
while todo:
    p = todo.pop()
    for lib in needed(p):
        if lib in seen or lib in LLNDK:
            continue
        seen.add(lib)
        if in_image(lib):
            continue
        dst = os.path.join(C, "vendor/lib64", lib)
        subprocess.run(["debugfs", "-R", f"dump /lib64/{lib} {dst}", V], capture_output=True)
        if os.path.exists(dst) and os.path.getsize(dst) > 0:
            os.chmod(dst, 0o644); copied.append(lib); todo.append(dst)
        else:
            if os.path.exists(dst): os.remove(dst)
            missing.append(lib)
print("[camera] 从模拟器拷的库：", " ".join(sorted(copied)) or "无")
if missing:
    print("[camera] 镜像和模拟器 vendor 里都没有：", " ".join(sorted(missing)))
    sys.exit(1)
EOF
log "摄像头服务准备好了：$(find "$C" -type f | wc -l) 个文件"
