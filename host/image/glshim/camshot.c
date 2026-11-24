// SPDX-License-Identifier: Apache-2.0
/*
 * pp-camshot：测试工具（踩坑 #152）。手机里列出摄像头（朝向、传感器像素），每个拍一张 JPEG 存到 /data/local/tmp/pp-cam<ID>.jpg，
 * 打印大小。用安卓 NDK 的摄像头接口（libcamera2ndk）+ AImageReader（libmediandk），和 App 走的是同一条路。
 * 不用 NDK：函数自己声明，链接空壳库（build.sh）。用法：pp-camshot [宽 高]（默认 1280 720）
 */
typedef unsigned char u8;
typedef int i32;
typedef unsigned int u32;
typedef long i64;

extern int printf(const char *f, ...);
extern void exit(int c);
extern int usleep(unsigned us);
extern int open(const char *p, int f, ...);
extern long write(int fd, const void *b, unsigned long n);
extern int close(int fd);
extern int atoi(const char *s);
extern int snprintf(char *b, unsigned long n, const char *f, ...);

typedef struct { int numCameras; const char **cameraIds; } ACameraIdList;
typedef struct { void *ctx; void (*onDisconnected)(void *, void *); void (*onError)(void *, void *, int); } DevCb;
typedef struct { void *ctx; void (*onClosed)(void *, void *); void (*onReady)(void *, void *); void (*onActive)(void *, void *); } SessCb;
typedef struct { u32 tag; u8 type; u32 count; union { const u8 *u8; const i32 *i32; const void *p; } data; } MetaEntry;

extern void *ACameraManager_create(void);
extern int ACameraManager_getCameraIdList(void *m, ACameraIdList **l);
extern int ACameraManager_getCameraCharacteristics(void *m, const char *id, void **meta);
extern int ACameraMetadata_getConstEntry(const void *meta, u32 tag, MetaEntry *e);
extern int ACameraManager_openCamera(void *m, const char *id, DevCb *cb, void **dev);
extern int ACameraDevice_close(void *dev);
extern int ACaptureSessionOutputContainer_create(void **c);
extern int ACaptureSessionOutput_create(void *win, void **o);
extern int ACaptureSessionOutputContainer_add(void *c, const void *o);
extern int ACameraDevice_createCaptureSession(void *dev, const void *c, const SessCb *cb, void **sess);
extern int ACameraDevice_createCaptureRequest(const void *dev, int tmpl, void **req);
extern int ACameraOutputTarget_create(void *win, void **t);
extern int ACaptureRequest_addTarget(void *req, const void *t);
extern int ACameraCaptureSession_capture(void *sess, void *cbs, int n, void **reqs, int *seq);
extern void ACameraCaptureSession_close(void *sess);
extern int AImageReader_new(i32 w, i32 h, i32 fmt, i32 max, void **r);
extern int AImageReader_getWindow(void *r, void **win);
extern int AImageReader_acquireNextImage(void *r, void **img);
extern int AImage_getPlaneData(const void *img, int plane, u8 **data, int *len);
extern void AImage_delete(void *img);
extern void ABinderProcess_setThreadPoolMaxThreadCount(u32 n);
extern void ABinderProcess_startThreadPool(void);

#define LENS_FACING 0x80005
#define PIXEL_ARRAY 0xF0006
#define FMT_JPEG 0x100

static void nop2(void *a, void *b) { (void)a; (void)b; }
static void nop3(void *a, void *b, int c) { (void)a; (void)b; (void)c; }

static int shot(void *mgr, const char *id, int w, int h) {
	void *dev = 0, *reader = 0, *win = 0, *cont = 0, *out = 0, *sess = 0, *req = 0, *tgt = 0, *img = 0;
	DevCb dcb = { 0, nop2, nop3 };
	SessCb scb = { 0, nop2, nop2, nop2 };
	int r;
	if ((r = ACameraManager_openCamera(mgr, id, &dcb, &dev)) != 0) { printf("  打开失败 %d\n", r); return 1; }
	if ((r = AImageReader_new(w, h, FMT_JPEG, 2, &reader)) != 0 || AImageReader_getWindow(reader, &win) != 0) { printf("  建图像接收失败 %d\n", r); return 1; }
	ACaptureSessionOutputContainer_create(&cont);
	ACaptureSessionOutput_create(win, &out);
	ACaptureSessionOutputContainer_add(cont, out);
	if ((r = ACameraDevice_createCaptureSession(dev, cont, &scb, &sess)) != 0) { printf("  建会话失败 %d\n", r); return 1; }
	ACameraDevice_createCaptureRequest(dev, 2 /* 拍照 */, &req);
	ACameraOutputTarget_create(win, &tgt);
	ACaptureRequest_addTarget(req, tgt);
	int seq = 0;
	if ((r = ACameraCaptureSession_capture(sess, 0, 1, &req, &seq)) != 0) { printf("  拍照请求失败 %d\n", r); return 1; }
	int i;
	for (i = 0; i < 100 && AImageReader_acquireNextImage(reader, &img) != 0; i++) usleep(100000);
	if (!img) { printf("  10 秒没拿到照片\n"); return 1; }
	u8 *data = 0; int len = 0;
	AImage_getPlaneData(img, 0, &data, &len);
	char path[64];
	snprintf(path, sizeof path, "/data/local/tmp/pp-cam%s.jpg", id);
	int fd = open(path, 0101 | 01000, 0644);
	if (fd >= 0) { write(fd, data, (unsigned long)len); close(fd); }
	printf("  拍到 %dx%d JPEG %d 字节（开头 %02x %02x），%d 毫秒 → %s\n", w, h, len, len > 1 ? data[0] : 0, len > 1 ? data[1] : 0, i * 100, path);
	AImage_delete(img);
	ACameraCaptureSession_close(sess);
	ACameraDevice_close(dev);
	return 0;
}

static int run(int argc, char **argv) {
	int w = argc > 2 ? atoi(argv[1]) : 1280, h = argc > 2 ? atoi(argv[2]) : 720;
	/* 图像接收器（AImageReader）的缓冲队列在本进程里，摄像头服务要通过 binder 回调过来取缓冲区：没有 binder 线程就一直等 */
	ABinderProcess_setThreadPoolMaxThreadCount(4);
	ABinderProcess_startThreadPool();
	void *mgr = ACameraManager_create();
	ACameraIdList *list = 0;
	if (!mgr || ACameraManager_getCameraIdList(mgr, &list) != 0 || !list) { printf("摄像头服务连不上\n"); return 1; }
	printf("摄像头 %d 个\n", list->numCameras);
	int i, bad = 0;
	for (i = 0; i < list->numCameras; i++) {
		const char *id = list->cameraIds[i];
		void *meta = 0;
		MetaEntry e;
		int facing = -1, pw = 0, ph = 0;
		if (ACameraManager_getCameraCharacteristics(mgr, id, &meta) == 0) {
			if (ACameraMetadata_getConstEntry(meta, LENS_FACING, &e) == 0 && e.count) facing = e.data.u8[0];
			if (ACameraMetadata_getConstEntry(meta, PIXEL_ARRAY, &e) == 0 && e.count >= 2) pw = e.data.i32[0], ph = e.data.i32[1];
		}
		printf("摄像头 %s：%s，传感器 %dx%d\n", id, facing == 0 ? "前置" : facing == 1 ? "后置" : "其他", pw, ph);
		bad += shot(mgr, id, w, h);
	}
	return bad;
}

/* 没有 crt：自己的入口，栈上是 argc、argv */
__attribute__((used)) void _start_c(long *sp) { exit(run((int)sp[0], (char **)(sp + 1))); }
__asm__(".globl _start\n_start:\n mov x0, sp\n b _start_c\n");
