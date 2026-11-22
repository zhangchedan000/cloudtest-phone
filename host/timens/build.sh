#!/bin/sh
# 编 pp-timens（arm64、不链接 libc、几 KB），产物放 agent/internal/timens/pp-timens-arm64（管家 go:embed 进去）。
# 在 arm64 机器（板子）上：sh host/timens/build.sh；有交叉编译器时 CC=aarch64-linux-gnu-gcc sh host/timens/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
CC=${CC:-gcc}
out=${1:-$here/../../agent/internal/timens/pp-timens-arm64}
$CC -static -nostdlib -ffreestanding -fno-builtin -fno-stack-protector -fno-asynchronous-unwind-tables -fno-pie -no-pie \
  -O2 -Wl,--build-id=none -s -o "$out" "$here/pp-timens.c"
ls -la "$out"
