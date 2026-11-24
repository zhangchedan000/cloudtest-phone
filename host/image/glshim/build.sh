#!/bin/sh
# 编译伪装档的 GLES 转接库（pp_glshim.c）：64 位 + 32 位，产物 out/arm64/libGLES_pp.so、out/arm/libGLES_pp.so。
# 不用 NDK：clang 直接按安卓目标编译，libc / libdl 用同名空壳库链接（运行时由手机里真正的 libc / libdl 提供）。
# 需要：clang + lld（Ubuntu：apt install clang lld）。
set -eu
cd "$(dirname "$0")"
rm -rf out && mkdir -p out/stub
cat > out/stub/stub.c <<'EOF'
void *dlopen(const char *p, int f) { return 0; }
void *dlsym(void *h, const char *n) { return 0; }
int open(const char *p, int f, ...) { return -1; }
long read(int fd, void *b, unsigned long n) { return -1; }
int close(int fd) { return -1; }
EOF
for arch in arm64:aarch64-linux-android29 arm:armv7a-linux-androideabi29; do
	name=${arch%%:*}; target=${arch#*:}
	mkdir -p out/$name
	for lib in libc libdl; do
		clang --target=$target -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,$lib.so -o out/stub/$name-$lib.so out/stub/stub.c
	done
	clang --target=$target -O2 -fPIC -shared -nostdlib -fuse-ld=lld -fvisibility=hidden -fno-stack-protector \
		-Wl,-soname,libGLES_pp.so -Wl,--hash-style=both -Wl,-z,noexecstack -Wl,--no-undefined \
		-o out/$name/libGLES_pp.so pp_glshim.c out/stub/$name-libdl.so out/stub/$name-libc.so
	llvm-strip --strip-unneeded out/$name/libGLES_pp.so 2>/dev/null || true
done
# 测试工具 pp-glinfo（64 位）：手机里打印 App 拿到的显卡字符串
cat > out/stub/gl.c <<'EOF2'
void *eglGetDisplay(void *a) { return 0; } unsigned eglInitialize(void *a, int *b, int *c) { return 0; }
unsigned eglChooseConfig(void *a, const int *b, void **c, int d, int *e) { return 0; } void *eglCreateContext(void *a, void *b, void *c, const int *d) { return 0; }
void *eglCreatePbufferSurface(void *a, void *b, const int *c) { return 0; } unsigned eglMakeCurrent(void *a, void *b, void *c, void *d) { return 0; }
const char *glGetString(unsigned a) { return 0; } const char *glGetStringi(unsigned a, unsigned b) { return 0; } void glGetIntegerv(unsigned a, int *b) {}
int printf(const char *f, ...) { return 0; } void exit(int c) {}
EOF2
for lib in libEGL libGLESv2 libc; do
	clang --target=aarch64-linux-android29 -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,$lib.so -o out/stub/gl-$lib.so out/stub/gl.c
done
clang --target=aarch64-linux-android29 -O2 -fno-builtin -fPIE -pie -nostdlib -fuse-ld=lld -Wl,--dynamic-linker=/system/bin/linker64 -Wl,-z,noexecstack \
	-o out/arm64/pp-glinfo glinfo.c out/stub/gl-libEGL.so out/stub/gl-libGLESv2.so out/stub/gl-libc.so -Wl,-e,_start
# 测试工具 pp-camshot（64 位）：手机里列摄像头、每个拍一张 JPEG（踩坑 #152）
cat > out/stub/cam.c <<'EOF3'
void *ACameraManager_create(void) { return 0; } int ACameraManager_getCameraIdList(void *m, void **l) { return 0; }
int ACameraManager_getCameraCharacteristics(void *m, const char *id, void **x) { return 0; } int ACameraMetadata_getConstEntry(const void *m, unsigned t, void *e) { return 0; }
int ACameraManager_openCamera(void *m, const char *id, void *cb, void **d) { return 0; } int ACameraDevice_close(void *d) { return 0; }
int ACaptureSessionOutputContainer_create(void **c) { return 0; } int ACaptureSessionOutput_create(void *w, void **o) { return 0; }
int ACaptureSessionOutputContainer_add(void *c, const void *o) { return 0; } int ACameraDevice_createCaptureSession(void *d, const void *c, const void *cb, void **s) { return 0; }
int ACameraDevice_createCaptureRequest(const void *d, int t, void **r) { return 0; } int ACameraOutputTarget_create(void *w, void **t) { return 0; }
int ACaptureRequest_addTarget(void *r, const void *t) { return 0; } int ACameraCaptureSession_capture(void *s, void *c, int n, void **r, int *q) { return 0; }
void ACameraCaptureSession_close(void *s) {}
EOF3
cat > out/stub/media.c <<'EOF3'
int AImageReader_new(int w, int h, int f, int m, void **r) { return 0; } int AImageReader_getWindow(void *r, void **w) { return 0; }
int AImageReader_acquireNextImage(void *r, void **i) { return 0; } int AImage_getPlaneData(const void *i, int p, void **d, int *l) { return 0; } void AImage_delete(void *i) {}
EOF3
cat > out/stub/camlibc.c <<'EOF3'
int printf(const char *f, ...) { return 0; } void exit(int c) {} int usleep(unsigned u) { return 0; } int open(const char *p, int f, ...) { return 0; }
long write(int fd, const void *b, unsigned long n) { return 0; } int close(int fd) { return 0; } int atoi(const char *s) { return 0; }
int snprintf(char *b, unsigned long n, const char *f, ...) { return 0; }
EOF3
cat > out/stub/binder.c <<'EOF3'
void ABinderProcess_setThreadPoolMaxThreadCount(unsigned n) {} void ABinderProcess_startThreadPool(void) {}
EOF3
clang --target=aarch64-linux-android29 -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,libbinder_ndk.so -o out/stub/libbinder_ndk.so out/stub/binder.c
clang --target=aarch64-linux-android29 -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,libcamera2ndk.so -o out/stub/libcamera2ndk.so out/stub/cam.c
clang --target=aarch64-linux-android29 -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,libmediandk.so -o out/stub/libmediandk.so out/stub/media.c
clang --target=aarch64-linux-android29 -fPIC -shared -nostdlib -fuse-ld=lld -Wl,-soname,libc.so -o out/stub/cam-libc.so out/stub/camlibc.c
clang --target=aarch64-linux-android29 -O2 -fno-builtin -fPIE -pie -nostdlib -fuse-ld=lld -Wl,--dynamic-linker=/system/bin/linker64 -Wl,-z,noexecstack \
	-o out/arm64/pp-camshot camshot.c out/stub/libcamera2ndk.so out/stub/libmediandk.so out/stub/libbinder_ndk.so out/stub/cam-libc.so -Wl,-e,_start
rm -rf out/stub
ls -la out/*/libGLES_pp.so out/arm64/pp-glinfo out/arm64/pp-camshot
