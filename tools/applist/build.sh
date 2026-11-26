#!/usr/bin/env bash
# 编译应用列表小工具 Apps.java → pp-apps.dex（放进 agent/internal/phones/，管家 go:embed 内嵌，编译结果提交进仓库）。
# 和剪贴板小工具 tools/clipper/build.sh 一个路子，区别：Apps.java 用到 Intent/PackageManager/Bitmap 等公开 API，
# 编译时要对着 android.jar（安卓 SDK 的 API 桩）；运行时用容器里真正的框架。
#
# 在任何装了 Docker 的 Linux 上跑：bash tools/applist/build.sh
# 不在宿主机装 JDK：用临时的 eclipse-temurin 容器；dex 编译器 d8 来自 r8（固定版本、核对 sha256）；
# android.jar 用 Sable 的 android-34 桩（固定、核对 sha256）。
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../.." && pwd)

R8_VERSION=8.13.25
R8_SHA256=${R8_SHA256:-0d4f2f68c4fcbc9434eff122f41feb6fb1d98efd2faa530cec1abacab1cea03d}
R8_URL="https://dl.google.com/android/maven2/com/android/tools/r8/$R8_VERSION/r8-$R8_VERSION.jar"

# android.jar：安卓 34(API 34) 的编译桩。Sable/android-platforms 是常用的公开镜像。
ANDROID_API=34
ANDROID_JAR_SHA256=${ANDROID_JAR_SHA256:-6cea1df3efb77103ac3e2beb9bf4718964b0e0869ab16d39d29d5cbae1c147ad}
ANDROID_JAR_URL="https://raw.githubusercontent.com/Sable/android-platforms/master/android-$ANDROID_API/android.jar"

OUT="$ROOT/agent/internal/phones/pp-apps.dex"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fetch() { # url out sha256
  curl -fsSL --retry 3 -o "$2" "$1"
  local got; got=$(sha256sum "$2" | cut -d' ' -f1)
  if [ -n "$3" ] && [ "$got" != "$3" ]; then echo "$2 的 sha256 不对：$got（期望 $3）" >&2; exit 1; fi
  echo "$(basename "$2") sha256 $got"
}

fetch "$R8_URL" "$WORK/r8.jar" "$R8_SHA256"
fetch "$ANDROID_JAR_URL" "$WORK/android.jar" "$ANDROID_JAR_SHA256"
cp "$HERE/Apps.java" "$WORK/"

build() { # 在给的环境里编译：$1=javac $2=java
  "$1" --release 8 -encoding UTF-8 -classpath "$WORK/android.jar" -d "$WORK/classes" "$WORK/Apps.java"
  "$2" -cp "$WORK/r8.jar" com.android.tools.r8.D8 --release --min-api 26 --lib "$WORK/android.jar" \
    --output "$WORK/out" "$WORK"/classes/*.class
}

mkdir -p "$WORK/classes" "$WORK/out"
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  docker run --rm -v "$WORK:/w" -w /w eclipse-temurin:17-jdk sh -c '
    mkdir -p classes out
    javac --release 8 -encoding UTF-8 -classpath android.jar -d classes Apps.java
    java -cp r8.jar com.android.tools.r8.D8 --release --min-api 26 --lib android.jar --output out classes/*.class
  '
elif command -v javac >/dev/null 2>&1 && command -v java >/dev/null 2>&1; then
  echo "没有 docker，用宿主机的 javac/java"
  build javac java
else
  echo "既没有 docker，也没有 javac/java，装一个再来" >&2; exit 1
fi

cp "$WORK/out/classes.dex" "$OUT"
echo "编译完成：$OUT（$(stat -c %s "$OUT") 字节，sha256 $(sha256sum "$OUT" | cut -d' ' -f1)）"
