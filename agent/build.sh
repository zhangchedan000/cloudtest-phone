#!/usr/bin/env bash
# 编译管家程序：先把 profiles/ 和 web/dist 复制进来打包，再编译 ARM64 单文件。
# 用法： bash agent/build.sh [输出路径]   （需要 Go 1.22+；web/dist 要先 npm run build）
set -euo pipefail
cd "$(dirname "$0")"
root=..
rm -rf internal/assets/profiles internal/assets/web internal/assets/tools
cp -r "$root/profiles" internal/assets/profiles
# 导入机型包要用的脚本（管理页「导入机型包」由管家调 python3 跑）
mkdir -p internal/assets/tools && cp "$root/host/models/import-model.py" "$root/host/extract-buildprops.py" internal/assets/tools/
# 伪装档的显卡字符串转接库（安卓库，clang + lld 编；编不了这台机器的管家就不支持伪装档，其余照常）
if sh "$root/host/image/glshim/build.sh" >/dev/null 2>&1; then cp "$root/host/image/glshim/out/arm64/libGLES_pp.so" internal/assets/tools/
else echo "警告：显卡转接库没编出来（要 clang + lld），伪装档机型开机时显卡名不会改" >&2; fi
if sh "$root/host/image/sensorshim/build.sh" >/dev/null 2>&1; then cp "$root/host/image/sensorshim/out/arm64/libsensors_pp.so" internal/assets/tools/
else echo "警告：传感器转接库没编出来（要 clang + lld），传感器清单不会照真机" >&2; fi
if [ -d "$root/web/dist" ]; then cp -r "$root/web/dist" internal/assets/web; else mkdir -p internal/assets/web && echo '<p>管理页没有编译（web/dist 不存在）</p>' > internal/assets/web/index.html; fi
ver=$(git -C "$root" describe --always --dirty 2>/dev/null || echo dev)
out=${1:-bin/prism-phone-agent}
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$out" .
echo "编译完成：$out（$ver）"
