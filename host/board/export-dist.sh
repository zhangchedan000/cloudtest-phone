#!/bin/sh
# 在【正在用的板子】上把换板要的复用件统一打包到 /mmc/pp-dist（换新板时把这个文件夹整个拷走）：
#   android-images.tar.gz     管家在用的安卓镜像（13/14/15/16 普通 + 14/15/16 谷歌，从 /etc/conf.d/prism-phone-agent 读）
#   build-sources.tar.gz      （加 --with-build-sources 才导）重建镜像用的底子：魔云腾 dobox Q14/Q15/Q16 + 官方 redroid 底子
#   mihomo-arm64.gz           网络模块程序
#   pp-rootcanal-arm64-bundle.tgz  蓝牙芯片模拟器（带 glibc 加载器，踩坑 #122；已有就不动）
#   MANIFEST.txt              清单 + sha256（新板 bootstrap.sh 按它校验）
# 用法（root）：sh host/board/export-dist.sh [--with-build-sources]
# 镜像之间共用层，一次 docker save 多个只存一份；pigz 多核压缩。7 个在用镜像压完约 6~8G，要十几分钟。
set -u
D=/mmc/pp-dist
WITH_SRC=0; [ "${1:-}" = --with-build-sources ] && WITH_SRC=1
say() { echo "$(date '+%T') $*"; }
die() { say "✗ $*"; exit 1; }
[ "$(id -u)" = 0 ] || die "请用 root 运行"
command -v pigz >/dev/null || apk add -q pigz || die "缺 pigz"
mkdir -p "$D"

ARGS=$(sed -n 's/^PP_ARGS="\(.*\)"$/\1/p' /etc/conf.d/prism-phone-agent 2>/dev/null)
[ -n "$ARGS" ] || die "读不到 /etc/conf.d/prism-phone-agent 的 PP_ARGS"
IMGS=$(echo "$ARGS" | tr ' ' '\n' | awk '/^-images$|^-gapps-images$/{getline; print}' | tr ',' '\n' | cut -d= -f2 | sort -u)
for i in $IMGS; do docker image inspect "$i" >/dev/null 2>&1 || die "镜像不在：$i"; done
free=$(df -m /mmc | awk 'NR==2{print $4}')
[ "$free" -gt 30000 ] || die "/mmc 只剩 ${free}M，至少要 30G"

say "导出在用的安卓镜像（$(echo "$IMGS" | wc -l) 个）→ $D/android-images.tar.gz"
# shellcheck disable=SC2086
docker save $IMGS | pigz -1 > "$D/android-images.tar.gz.part" && mv "$D/android-images.tar.gz.part" "$D/android-images.tar.gz" || die "导出镜像失败"
echo "$IMGS" > "$D/android-images.list"

if [ "$WITH_SRC" = 1 ]; then
  SRC=$(docker images --format '{{.Repository}}:{{.Tag}}' | grep -E 'mytos/dobox:|^redroid/redroid:1[456]\.0\.0_(64only-latest|mindthegapps)$' | sort)
  say "导出重建用底子（$(echo "$SRC" | wc -l) 个）→ $D/build-sources.tar.gz"
  # shellcheck disable=SC2086
  docker save $SRC | pigz -1 > "$D/build-sources.tar.gz.part" && mv "$D/build-sources.tar.gz.part" "$D/build-sources.tar.gz" || die "导出底子失败"
  echo "$SRC" > "$D/build-sources.list"
fi

say "mihomo → $D/mihomo-arm64.gz"
pigz -9 -c /usr/local/bin/mihomo > "$D/mihomo-arm64.gz"
[ -f "$D/pp-rootcanal-arm64-bundle.tgz" ] || say "⚠ 没有蓝牙芯片模拟器包 pp-rootcanal-arm64-bundle.tgz（新板上手机蓝牙会关着）"

say "写清单"
{
  echo "# 云测机换板复用件（$(date '+%F %T')，来自 $(hostname)，管家 $(/opt/prism-phone/prism-phone-agent -version 2>/dev/null)）"
  echo "# 新板：把整个 /mmc/pp-dist 拷过去，跑 host/board/bootstrap.sh"
  cd "$D" && sha256sum android-images.tar.gz mihomo-arm64.gz pp-rootcanal-arm64-bundle.tgz build-sources.tar.gz 2>/dev/null
} > "$D/MANIFEST.txt"
# 旧的单镜像包 / 旧配置包已被 android-images.tar.gz 和仓库 host/board 取代
rm -f "$D/android13-full.tar.gz" "$D/board-setup.tar.gz" "$D"/*.log "$D/images-before.txt" "$D/nft-backup.txt"
ls -la "$D"
say "完成。换板时把 $D 整个拷走（U 盘 / scp）"
