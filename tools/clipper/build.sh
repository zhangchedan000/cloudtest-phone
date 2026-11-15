#!/usr/bin/env bash
# 编译剪贴板小工具 Clip.java → pp-clip.dex（放进 agent/internal/phones/，管家程序 go:embed 内嵌，编译结果提交进仓库）。
# 在任何装了 Docker 的 Linux 上跑：bash tools/clipper/build.sh
# 不在宿主机装 JDK：用临时的 eclipse-temurin 容器；dex 编译器 d8 来自谷歌官方 Maven 的 r8（固定版本、核对 sha256）。
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)
R8_VERSION=8.13.25
R8_SHA256=${R8_SHA256:-0d4f2f68c4fcbc9434eff122f41feb6fb1d98efd2faa530cec1abacab1cea03d}   # 2026-11-15 第一次下载时记下
OUT="$ROOT/agent/internal/phones/pp-clip.dex"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

curl -fsSL --retry 3 -o "$WORK/r8.jar" "https://dl.google.com/android/maven2/com/android/tools/r8/$R8_VERSION/r8-$R8_VERSION.jar"
got=$(sha256sum "$WORK/r8.jar" | cut -d' ' -f1)
if [ -n "$R8_SHA256" ] && [ "$got" != "$R8_SHA256" ]; then
  echo "r8.jar 的 sha256 不对：$got" >&2; exit 1
fi
echo "r8 $R8_VERSION sha256 $got"
cp "$HERE/Clip.java" "$WORK/"
docker run --rm -v "$WORK:/w" -w /w eclipse-temurin:17-jdk sh -c '
  javac --release 8 -encoding UTF-8 -d classes Clip.java 2>&1 | grep -v "warning: \[options\]" || true
  mkdir -p out
  java -cp r8.jar com.android.tools.r8.D8 --release --min-api 26 --output out classes/Clip.class
'
cp "$WORK/out/classes.dex" "$OUT"
echo "编译完成：$OUT（$(stat -c %s "$OUT") 字节）"
