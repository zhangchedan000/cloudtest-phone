# 镜像里去掉 Redroid 痕迹（踩坑 #63）：在 docker build 里用临时的静态 busybox 跑（安卓镜像里的 sh 依赖 /apex，构建时用不了）。
# 用法：busybox sh fixup-redroid-names.sh "mt6886 mt6895 mt6896"
#   - 显卡驱动 gralloc / hwcomposer 按每个芯片名各放一份（管家开机用 androidboot.hardware=<芯片名>，安卓按这个名字找驱动），删掉 *.redroid.so；
#   - redroid.*.rc、*.redroid.sh 换成普通名字（引用一起改），老参数兼容的 redroid.legacy.rc、空的 init.redroid.rc 删掉；
#   - gpu_config.sh 里 ro.hardware.gralloc 不再写死 redroid，跟着 ro.hardware；
#   - vendor/build.prop 去掉 ro.boot.use_redroid_c2（编解码设置改成无条件做）。
set -e
HWS="$1"
V=/vendor
[ -n "$HWS" ] || { echo "没给芯片名"; exit 1; }
for d in lib64 lib; do
  for m in gralloc hwcomposer; do
    f=$V/$d/hw/$m.redroid.so
    [ -f "$f" ] || continue
    for hw in $HWS; do cp -a "$f" "$V/$d/hw/$m.$hw.so"; done
    rm -f "$f"
  done
done
cd $V/etc/init
if [ -f redroid.common.rc ]; then
  sed -e 's#/vendor/bin/post-fs-data.redroid.sh#/vendor/bin/post-fs-data.sh#' -e 's/use_redroid_overlayfs/use_overlayfs/g' redroid.common.rc > init.platform.rc
  rm -f redroid.common.rc
fi
if [ -f redroid.c2.rc ]; then
  sed -e 's#^on init && property:ro.boot.use_redroid_c2=1#on init#' -e 's#/vendor/bin/redroid.c2.sh#/vendor/bin/c2_setup.sh#' redroid.c2.rc > init.codec2.rc
  rm -f redroid.c2.rc
fi
rm -f redroid.legacy.rc init.redroid.rc
[ -f $V/bin/post-fs-data.redroid.sh ] && mv $V/bin/post-fs-data.redroid.sh $V/bin/post-fs-data.sh
[ -f $V/bin/redroid.c2.sh ] && mv $V/bin/redroid.c2.sh $V/bin/c2_setup.sh
[ -f $V/bin/gpu_config.sh ] && sed -i 's/setprop ro.hardware.gralloc redroid/setprop ro.hardware.gralloc $(getprop ro.hardware)/' $V/bin/gpu_config.sh
[ -f $V/etc/permissions/redroid-removed-permissions.xml ] && mv $V/etc/permissions/redroid-removed-permissions.xml $V/etc/permissions/removed-permissions.xml
rm -f $V/etc/hwdata/amdgpu.ids.redroid
sed -i '/use_redroid_c2/d' $V/build.prop
# 电话打开（踩坑 #65）：去掉 ro.radio.noril=yes；有模拟器的新版 rild 就去掉 Redroid 自带的老 rild（服务同名会冲突）
sed -i '/ro.radio.noril/d' $V/build.prop
if [ -f $V/etc/init/rild_goldfish.rc ]; then rm -f $V/etc/init/rild.rc $V/bin/hw/rild; fi
# 蓝牙（踩坑 #48）：有模拟器的串口蓝牙 HAL 就去掉 Redroid 自带的坏 HAL（内嵌的 rootcanal 一启动就崩）
if [ -f $V/bin/hw/android.hardware.bluetooth-service.default ]; then
  rm -f $V/etc/init/android.hardware.bluetooth@1.1-service.sim.rc $V/bin/hw/android.hardware.bluetooth@1.1-service.sim
fi
echo "redroid 名字处理完：$HWS"
find / -xdev -iname '*redroid*' 2>/dev/null | grep -v '^/proc' || true
