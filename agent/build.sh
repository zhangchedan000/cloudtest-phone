#!/usr/bin/env bash
# 编译管家程序：先把 profiles/ 和 web/dist 复制进来打包，再编译 ARM64 单文件。
# 用法： bash agent/build.sh [输出路径]   （需要 Go 1.22+；web/dist 要先 npm run build）
set -euo pipefail
cd "$(dirname "$0")"
root=..
rm -rf internal/assets/profiles internal/assets/web
cp -r "$root/profiles" internal/assets/profiles
if [ -d "$root/web/dist" ]; then cp -r "$root/web/dist" internal/assets/web; else mkdir -p internal/assets/web && echo '<p>管理页没有编译（web/dist 不存在）</p>' > internal/assets/web/index.html; fi
ver=$(git -C "$root" describe --always --dirty 2>/dev/null || echo dev)
out=${1:-bin/prism-phone-agent}
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$out" .
echo "编译完成：$out（$ver）"
