#!/bin/sh
# 编译传感器转接库 pp_sensorshim.c（64 位）：产物 out/arm64/libsensors_pp.so。和 glshim 一样不用 NDK（clang + lld）。
set -eu
cd "$(dirname "$0")"
rm -rf out && mkdir -p out/stub out/arm64
cat > out/stub/libc.c <<'S'
int open(const char *p, int f, ...) { return -1; }
int close(int fd) { return -1; }
float strtof(const char *s, char **e) { return 0; }
long strtol(const char *s, char **e, int b) { return 0; }
S
cat > out/stub/libdl.c <<'S'
void *dlsym(void *h, const char *n) { return 0; }
void *android_dlopen_ext(const char *n, int f, const void *i) { return 0; }
int dl_iterate_phdr(void *cb, void *d) { return 0; }
S
t=aarch64-linux-android29
for lib in libc libdl; do
	clang --target=$t -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,$lib.so -o out/stub/$lib.so out/stub/$lib.c
done
clang --target=$t -O2 -fPIC -shared -nostdlib -fuse-ld=lld -fvisibility=hidden -fno-stack-protector -fno-builtin \
	-Wl,-soname,libsensors_pp.so -Wl,--hash-style=both -Wl,-z,noexecstack -Wl,--no-undefined -Wl,-z,max-page-size=4096 \
	-o out/arm64/libsensors_pp.so pp_sensorshim.c out/stub/libdl.so out/stub/libc.so
llvm-strip --strip-unneeded out/arm64/libsensors_pp.so 2>/dev/null || true
rm -rf out/stub
ls -la out/arm64/libsensors_pp.so
