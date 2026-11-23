#!/bin/sh
# 【新板子第一次装】魔云腾 Q1 / Alpine + OpenRC：刷完机、SSH 进去、把老板子的 /mmc/pp-dist 拷过来之后，跑这一个脚本。
#
#   wget -qO /tmp/bootstrap.sh https://raw.githubusercontent.com/zhangchedan000/cloudtest-phone/main/host/board/bootstrap.sh
#   sh /tmp/bootstrap.sh            # 装
#   sh /tmp/bootstrap.sh --check    # 只检查前提条件，不改任何东西
#
# 前提（脚本会先查）：root；Alpine；NVMe 大盘挂在 /mmc；内核有 binder（静态 /dev/binder1.. 或 binderfs）；
#   /mmc/pp-dist 里有老板子 export-dist.sh 导出的 android-images.tar.gz（mihomo 没有就从官方下、蓝牙包没有就从仓库下）。
# 做的事：apk 换阿里源 + 装软件包 → Docker（数据放 /mmc）→ 导入安卓镜像 → 装 mihomo / 小屏程序 →
#   生成访问口令 → 调 install.sh 装管家和全部系统配置 → 打印网页地址和口令。重复跑没关系（已做的跳过）。
# 刷机、改 root 口令、开 SSH 见 docs/上板流程.md；Desktop Commander 远程（可选）也见那里。
set -u
REPO=${PP_REPO:-zhangchedan000/cloudtest-phone}
REF=${PP_REF:-main}
RAW="https://raw.githubusercontent.com/$REPO/$REF"
D=/mmc/pp-dist
# mihomo：/mmc/pp-dist 里没有就从官方 GitHub 发布页下（和 Q1 上在用的是同一个文件，解压后 sha256 一致）
MIHOMO_URL=https://github.com/MetaCubeX/mihomo/releases/download/v1.19.34/mihomo-linux-arm64-v1.19.34.gz
MIHOMO_SHA=022da41e03ce70641a2f50c485b4f69ade8f1c7aeeece2cf1ec45c614a029441   # 解压后的程序
CHECK=0; [ "${1:-}" = --check ] && CHECK=1
say() { echo "$(date '+%T') $*"; }
die() { say "✗ $*"; exit 1; }
ok() { say "✓ $*"; }

# ---------- 0. 前提 ----------
bad=0
[ "$(id -u)" = 0 ] && ok "root" || { say "✗ 要 root"; bad=1; }
grep -q '^ID=alpine' /etc/os-release 2>/dev/null && ok "Alpine $(sed -n 's/^VERSION_ID=//p' /etc/os-release)" || { say "✗ 不是 Alpine（这个脚本只给 Alpine 板子用）"; bad=1; }
mountpoint -q /mmc 2>/dev/null && ok "/mmc 挂着，剩 $(df -h /mmc | awk 'NR==2{print $4}')" || { say "✗ /mmc 没挂（NVMe 大盘）"; bad=1; }
if [ -e /dev/binder1 ] || grep -q binder /proc/filesystems; then ok "内核有 binder"; else say "✗ 内核没有 binder（Redroid 跑不了）"; bad=1; fi
[ -f "$D/android-images.tar.gz" ] && ok "安卓镜像包在" || { say "✗ 没有 $D/android-images.tar.gz（从老板子拷 /mmc/pp-dist，老板子上先跑 host/board/export-dist.sh）"; bad=1; }
[ -f "$D/mihomo-arm64.gz" ] && ok "mihomo 包在" || say "· 没有 $D/mihomo-arm64.gz：装的时候从官方 GitHub 下（校验 sha256）"
[ -f "$D/pp-rootcanal-arm64-bundle.tgz" ] && ok "蓝牙芯片模拟器包在" || say "· 没有蓝牙芯片模拟器包：装的时候从仓库 tools/dist 下（没有就手机蓝牙关着，别的照常）"
if [ -f "$D/MANIFEST.txt" ] && command -v sha256sum >/dev/null; then
  # 不用 -s / --status：busybox 和 coreutils 的 sha256sum 参数不一样，只看退出码
  if (cd "$D" && grep -E '^[0-9a-f]{64} ' MANIFEST.txt | sha256sum -c >/dev/null 2>&1); then ok "复用件校验通过（MANIFEST.txt）"; else say "✗ 复用件校验不过（拷坏了？在 $D 里跑 sha256sum -c MANIFEST.txt 看哪个）"; bad=1; fi
fi
[ -e /sys/class/pwm/pwmchip1 ] && ok "风扇 PWM（pwmchip1）在" || say "· 没有 pwmchip1（不是 Q1？风扇脚本要按板子改）"
[ "$bad" = 0 ] || die "前提条件不满足，见上面 ✗"
[ "$CHECK" = 1 ] && { say "检查通过（--check 只查不改）"; exit 0; }

# ---------- 1. 软件包 ----------
grep -q mirrors.aliyun.com /etc/apk/repositories 2>/dev/null || {
  v=$(sed -n 's/^VERSION_ID=\([0-9]*\.[0-9]*\).*/\1/p' /etc/os-release)
  printf 'https://mirrors.aliyun.com/alpine/v%s/main\nhttps://mirrors.aliyun.com/alpine/v%s/community\n' "$v" "$v" > /etc/apk/repositories
  say "apk 换阿里源"
}
apk update -q || die "apk update 失败（网络？）"
apk add -q docker nftables logrotate curl git python3 pigz e2fsprogs e2fsprogs-extra util-linux iw hostapd dnsmasq iproute2 || die "装软件包失败"
ok "软件包"

# ---------- 2. Docker（数据放 /mmc，根分区只有 3.5G，踩坑 #119） ----------
if [ ! -f /etc/docker/daemon.json ]; then
  mkdir -p /etc/docker
  cat > /etc/docker/daemon.json <<'EOF'
{
    "userland-proxy": false,
    "log-driver": "json-file",
    "log-opts": { "max-size": "5m", "max-file": "3" },
    "bip": "10.6.157.1/24",
    "data-root": "/mmc"
}
EOF
  say "写 /etc/docker/daemon.json（data-root=/mmc）"
fi
rc-update show default | grep -q '^ *docker ' || rc-update add docker default
rc-update show default | grep -q '^ *local ' || rc-update add local default
rc-service docker status >/dev/null 2>&1 || rc-service docker start
i=0; while ! docker info >/dev/null 2>&1 && [ $i -lt 30 ]; do sleep 1; i=$((i + 1)); done
docker info >/dev/null 2>&1 || die "docker 起不来"
ok "Docker"

# ---------- 3. 安卓镜像 ----------
need=0
if [ -f "$D/android-images.list" ]; then
  for img in $(cat "$D/android-images.list"); do docker image inspect "$img" >/dev/null 2>&1 || need=1; done
else
  need=1
fi
if [ "$need" = 1 ]; then
  say "导入安卓镜像（几分钟）"
  pigz -dc "$D/android-images.tar.gz" | docker load | tail -3 || die "导入镜像失败"
fi
[ -f "$D/build-sources.tar.gz" ] && ! docker images | grep -q 'mytos/dobox' && { say "导入重建用底子"; pigz -dc "$D/build-sources.tar.gz" | docker load | tail -2; }
# 导出包比仓库旧时补上镜像级的小改动（重复跑会跳过）：安卓 16 电话 HAL 属性名去 qemu 字样（踩坑 #127）
if curl -fsSL --retry 3 -o /tmp/pp-patch-modem-prop.sh "$RAW/host/image/patch-modem-prop.sh"; then
  for img in $(cat "$D/android-images.list" 2>/dev/null); do
    case "$img" in *:16-*) PP_BUILD=/mmc/pp-build sh /tmp/pp-patch-modem-prop.sh "$img" || say "· $img 补丁没打上（不影响开机）" ;; esac
  done
fi
# 实时画面硬件 H.264 编码（踩坑 #133）：从导入的魔云腾 dobox 同版本镜像里取 Rockchip Codec2 叠进安卓 14/15/16 镜像
if docker images | grep -q 'mytos/dobox' && curl -fsSL --retry 3 -o /tmp/pp-add-hwcodec.sh "$RAW/host/image/add-hwcodec.sh"; then
  for img in $(cat "$D/android-images.list" 2>/dev/null); do
    v=$(echo "$img" | sed -n 's/^prism-redroid-mali:\(1[456]\)-.*/\1/p')
    [ -n "$v" ] || continue
    case "$img" in *-orig*) continue ;; esac
    PP_BUILD=/mmc/pp-build sh /tmp/pp-add-hwcodec.sh "$img" "$v" || say "· $img 没加上硬件编码（实时画面用软件编码，不影响开机）"
  done
fi
# 手机里开真 SELinux（踩坑 #135）：安卓 14/15/16 镜像换回原版 SELinux 相关的库、补规则、给每层打标签。
# 要在硬件编码之后做（标签要打到所有层上）；做过的跳过。导出包里的镜像是做好的，但 docker save / load 会丢文件标签，
# 这里会自动按镜像自己的规则重打（不用 dobox）；没做过的镜像才要魔云腾 dobox 镜像（取原版库）
if true; then
  mkdir -p /tmp/pp-sel /mmc/pp-build
  if curl -fsSL --retry 3 -o /tmp/pp-sel/add-selinux.sh "$RAW/host/image/add-selinux.sh" &&
     curl -fsSL --retry 3 -o /tmp/pp-sel/sepatch.c "$RAW/host/image/sepatch.c" &&
     curl -fsSL --retry 3 -o /tmp/pp-sel/selinux-rules.txt "$RAW/host/image/selinux-rules.txt"; then
    for img in $(cat "$D/android-images.list" 2>/dev/null); do
      v=$(echo "$img" | sed -n 's/^prism-redroid-mali:\(1[456]\)-.*/\1/p')
      [ -n "$v" ] || continue
      case "$img" in *-orig*) continue ;; esac
      PP_BUILD=/mmc/pp-build sh /tmp/pp-sel/add-selinux.sh "$img" "$v" >> /mmc/pp-build/add-selinux.log 2>&1 ||
        say "· $img 没做成 SELinux 版（这个版本的手机照旧不开 SELinux，看 /mmc/pp-build/add-selinux.log）"
    done
  fi
fi
ok "安卓镜像"

# ---------- 4. mihomo / 小屏程序 ----------
if [ ! -x /usr/local/bin/mihomo ]; then
  if [ ! -f "$D/mihomo-arm64.gz" ]; then
    say "从官方 GitHub 下 mihomo"
    mkdir -p "$D"
    curl -fsSL --retry 3 -m 600 -o "$D/mihomo-arm64.gz.part" "$MIHOMO_URL" || die "下载 mihomo 失败（网络？）"
    [ "$(pigz -dc "$D/mihomo-arm64.gz.part" | sha256sum | cut -d' ' -f1)" = "$MIHOMO_SHA" ] || { rm -f "$D/mihomo-arm64.gz.part"; die "mihomo 校验不对"; }
    mv "$D/mihomo-arm64.gz.part" "$D/mihomo-arm64.gz"
  fi
  pigz -dc "$D/mihomo-arm64.gz" > /usr/local/bin/mihomo && chmod 755 /usr/local/bin/mihomo || die "装 mihomo 失败"
fi
/usr/local/bin/mihomo -v >/dev/null 2>&1 || die "mihomo 跑不起来"
mkdir -p /mmc/pp-data/mihomo
if [ ! -x /usr/local/bin/pp-oled ]; then
  curl -fsSL --retry 3 "$RAW/tools/dist/pp-oled-arm64.gz" | gunzip > /usr/local/bin/pp-oled && chmod 755 /usr/local/bin/pp-oled || say "· 小屏程序没装上（不影响别的）"
fi
ok "mihomo / 小屏程序"

# ---------- 5. 访问口令（第一次生成随机的） ----------
CONF=/etc/conf.d/prism-phone-agent
if [ ! -f "$CONF" ]; then
  tok=$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | cut -c1-24)
  args=$(curl -fsSL --retry 3 "$RAW/host/board/etc/conf.d/prism-phone-agent.example" | sed -n 's/^PP_ARGS="\(.*\)"$/\1/p')
  [ -n "$args" ] || die "取不到默认启动参数"
  (umask 077; printf 'PP_TOKEN="%s"\nPP_ARGS="%s"\n' "$tok" "$args" > "$CONF")
  say "生成访问口令（存在 $CONF，只有 root 能读）"
fi
chmod 600 "$CONF"

# ---------- 6. 管家 + 全部系统配置 ----------
curl -fsSL --retry 3 -o /tmp/pp-install.sh "$RAW/host/board/install.sh" || die "下载 install.sh 失败"
PP_REPO=$REPO sh /tmp/pp-install.sh "$REF" || die "install.sh 失败，看 /mmc/pp-install.log"

ip=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')
tok=$(sed -n 's/^PP_TOKEN="\(.*\)"$/\1/p' "$CONF")
echo
say "===== 装好了 ====="
say "管理页：http://${ip:-板子IP}:8099"
say "访问口令：$tok"
say "以后更新：sh /tmp/pp-install.sh（见 README「部署 → 板子」）"
