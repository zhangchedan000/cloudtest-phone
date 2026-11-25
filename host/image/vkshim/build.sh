#!/bin/sh
# 编译伪装档的 Vulkan 转接库（pp_vkshim.c）：64 位（安卓 Vulkan 驱动都是 64 位）。产物 out/arm64/libvulkan_pp.so。
# 不用 NDK：clang 直接按安卓目标编译，libc / libdl 用同名空壳库链接（运行时由手机里真正的库提供）。
# 需要：clang + lld。
set -eu
cd "$(dirname "$0")"
rm -rf out && mkdir -p out/stub out/arm64
cat > out/stub/stub.c <<'EOF'
void *dlopen(const char *p, int f) { return 0; }
void *dlsym(void *h, const char *n) { return 0; }
unsigned long strlen(const char *s) { return 0; }
int strcmp(const char *a, const char *b) { return 0; }
void *memcpy(void *d, const void *s, unsigned long n) { return d; }
EOF
target=aarch64-linux-android29
for lib in libc libdl; do
	clang --target=$target -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,$lib.so -o out/stub/$lib.so out/stub/stub.c
done
clang --target=$target -O2 -fPIC -shared -nostdlib -fuse-ld=lld -fvisibility=hidden -fno-stack-protector \
	-Wno-incompatible-function-pointer-types -Wno-incompatible-pointer-types \
	-Wl,-soname,libvulkan_pp.so -Wl,--hash-style=both -Wl,-z,noexecstack -Wl,--no-undefined \
	-o out/arm64/libvulkan_pp.so pp_vkshim.c out/stub/libdl.so out/stub/libc.so
llvm-strip --strip-unneeded out/arm64/libvulkan_pp.so 2>/dev/null || true
rm -rf out/stub
ls -la out/arm64/libvulkan_pp.so
# 自检：HMI 符号在、PPVKCFG1 标记在
if command -v llvm-nm >/dev/null 2>&1; then llvm-nm -D out/arm64/libvulkan_pp.so | grep -q " HMI$" && echo "✓ 导出 HMI" || echo "✗ 没导出 HMI"; fi
grep -q PPVKCFG1 out/arm64/libvulkan_pp.so && echo "✓ PPVKCFG1 标记在" || echo "✗ 缺 PPVKCFG1"
