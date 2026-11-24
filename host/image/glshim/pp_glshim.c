// SPDX-License-Identifier: Apache-2.0
/*
 * pp_glshim：伪装档机型的 GLES 转接库（2026-11-24）。
 *
 * 安卓的 EGL 加载器按 ro.hardware.egl / ro.board.platform 找 /vendor/lib64/egl/libGLES_<名>.so。伪装档机型的名字
 * （adreno、meow、pineapple…）指向这个库；它再打开板子真正的驱动 libGLES_mali.so，所有调用原样转过去，只改这几个：
 *   glGetString(GL_VENDOR / GL_RENDERER / GL_VERSION / GL_SHADING_LANGUAGE_VERSION / GL_EXTENSIONS)
 *   glGetStringi(GL_EXTENSIONS, i)、glGetIntegerv(GL_NUM_EXTENSIONS)
 * 返回机型包里录的真机值（显卡厂商、显卡名、驱动版本串、扩展列表）。
 *
 * 加载器的规矩：先 dlsym 驱动里的函数，没有就问驱动的 eglGetProcAddress。所以这里只导出 eglGetProcAddress 和要改的
 * 那几个 gl 函数，其余全部经 eglGetProcAddress 转给真驱动。真机扩展里有、Mali 驱动没有的函数，返回一个什么都不做
 * 的空函数（不然 Skia 等按扩展列表取函数取不到，会认为 GL 接口不完整、建不起绘图上下文）。
 *
 * 每台手机的真机值由管家写进下面的 cfg 区（找 "PPGLCFG1" 标记，后面依次是：1 字节开关、厂商、显卡名、版本串、
 * GLSL 版本串、扩展列表，各以 \0 结尾），写好的这份再挂到手机里对应的文件名上。cfg 没写（开关为 0）时完全透传。
 * 显示服务 surfaceflinger 不伪装（App 看不到它的显卡信息；按假扩展走它可能走错路径）。
 *
 * 不用 NDK：自己声明用到的几个 libc / libdl 函数，链接时用同名的空壳库（见 build.sh）。
 */
typedef unsigned int GLenum;
typedef unsigned int GLuint;
typedef int GLint;
typedef unsigned char GLubyte;
typedef void (*fn_t)(void);

extern void *dlopen(const char *path, int flags);
extern void *dlsym(void *handle, const char *name);
extern int open(const char *path, int flags, ...);
extern long read(int fd, void *buf, unsigned long n);
extern int close(int fd);

#define RTLD_NOW 2
#define GL_VENDOR 0x1F00
#define GL_RENDERER 0x1F01
#define GL_VERSION 0x1F02
#define GL_EXTENSIONS 0x1F03
#define GL_SHADING_LANGUAGE_VERSION 0x8B8C
#define GL_NUM_EXTENSIONS 0x821D

#ifdef __LP64__
#define REAL_DRIVER "/vendor/lib64/egl/libGLES_mali.so"
#else
#define REAL_DRIVER "/vendor/lib/egl/libGLES_mali.so"
#endif

#define EXPORT __attribute__((visibility("default")))
#define CFG_SIZE 32768
#define MAX_EXT 1024

/* 管家改写的区域：开头 8 字节标记，后面是开关 + 5 个字符串。volatile：别让编译器把内容当常量折叠掉 */
__attribute__((used, aligned(16))) volatile char pp_glshim_cfg[CFG_SIZE] = "PPGLCFG1";

static int inited;
static int spoof;
static void *real;
static fn_t (*real_gpa)(const char *);
static const GLubyte *(*real_getString)(GLenum);
static const GLubyte *(*real_getStringi)(GLenum, GLuint);
static void (*real_getIntegerv)(GLenum, GLint *);

static char buf[CFG_SIZE];      /* cfg 拷出来的字符串 */
static char extbuf[CFG_SIZE];   /* 扩展列表拆成一个个（glGetStringi 用） */
static const char *s_vendor, *s_renderer, *s_version, *s_glsl, *s_exts;
static const char *ext_list[MAX_EXT];
static int ext_n;

static int str_eq(const char *a, const char *b) {
	while (*a && *a == *b) a++, b++;
	return *a == *b;
}

static int ends_with(const char *s, const char *suf) {
	const char *a = s, *b = suf;
	while (*a) a++;
	while (*b) b++;
	while (b > suf && a > s) {
		if (*--a != *--b) return 0;
	}
	return b == suf;
}

static int is_surfaceflinger(void) {
	char cmd[256];
	int fd = open("/proc/self/cmdline", 0);
	if (fd < 0) return 0;
	long n = read(fd, cmd, sizeof(cmd) - 1);
	close(fd);
	if (n <= 0) return 0;
	cmd[n] = 0;
	return ends_with(cmd, "surfaceflinger");
}

static void init(void) {
	if (__atomic_load_n(&inited, __ATOMIC_ACQUIRE)) return;
	real = dlopen(REAL_DRIVER, RTLD_NOW);
	if (real) {
		real_gpa = (fn_t(*)(const char *))dlsym(real, "eglGetProcAddress");
		real_getString = (const GLubyte *(*)(GLenum))dlsym(real, "glGetString");
		real_getStringi = (const GLubyte *(*)(GLenum, GLuint))dlsym(real, "glGetStringi");
		real_getIntegerv = (void (*)(GLenum, GLint *))dlsym(real, "glGetIntegerv");
		if (!real_getStringi && real_gpa) real_getStringi = (const GLubyte *(*)(GLenum, GLuint))real_gpa("glGetStringi");
	}
	/* cfg：标记(8) 开关(1) 厂商\0 显卡名\0 版本\0 GLSL\0 扩展\0 */
	int on = pp_glshim_cfg[8] == 1;
	if (on && !is_surfaceflinger()) {
		int i;
		for (i = 0; i < CFG_SIZE - 9; i++) buf[i] = pp_glshim_cfg[9 + i];
		buf[CFG_SIZE - 10] = 0;
		const char *f[5];
		int k = 0, p = 0;
		while (k < 5 && p < CFG_SIZE - 9) {
			f[k++] = buf + p;
			while (p < CFG_SIZE - 9 && buf[p]) p++;
			p++;
		}
		if (k == 5 && *f[1]) {
			s_vendor = f[0], s_renderer = f[1], s_version = f[2], s_glsl = f[3], s_exts = f[4];
			/* 扩展列表按空格拆开 */
			int j = 0, q = 0;
			while (s_exts[j] && q < CFG_SIZE - 1) {
				while (s_exts[j] == ' ') j++;
				if (!s_exts[j]) break;
				if (ext_n < MAX_EXT) ext_list[ext_n++] = extbuf + q;
				while (s_exts[j] && s_exts[j] != ' ' && q < CFG_SIZE - 2) extbuf[q++] = s_exts[j++];
				extbuf[q++] = 0;
			}
			spoof = 1;
		}
	}
	__atomic_store_n(&inited, 1, __ATOMIC_RELEASE);
}

EXPORT const GLubyte *glGetString(GLenum name) {
	init();
	if (spoof) {
		switch (name) {
		case GL_VENDOR: return (const GLubyte *)s_vendor;
		case GL_RENDERER: return (const GLubyte *)s_renderer;
		case GL_VERSION: if (*s_version) return (const GLubyte *)s_version; break;
		case GL_SHADING_LANGUAGE_VERSION: if (*s_glsl) return (const GLubyte *)s_glsl; break;
		case GL_EXTENSIONS: return (const GLubyte *)s_exts;
		}
	}
	return real_getString ? real_getString(name) : 0;
}

EXPORT const GLubyte *glGetStringi(GLenum name, GLuint index) {
	init();
	if (spoof && name == GL_EXTENSIONS) return index < (GLuint)ext_n ? (const GLubyte *)ext_list[index] : 0;
	return real_getStringi ? real_getStringi(name, index) : 0;
}

EXPORT void glGetIntegerv(GLenum pname, GLint *data) {
	init();
	if (spoof && pname == GL_NUM_EXTENSIONS && data) {
		*data = ext_n;
		return;
	}
	if (real_getIntegerv) real_getIntegerv(pname, data);
}

/* 真机扩展里有、Mali 驱动没有的函数：什么都不做，返回 0 */
static long noop(void) { return 0; }

EXPORT fn_t eglGetProcAddress(const char *name) {
	init();
	if (!name) return 0;
	if (str_eq(name, "glGetString")) return (fn_t)glGetString;
	if (str_eq(name, "glGetStringi")) return (fn_t)glGetStringi;
	if (str_eq(name, "glGetIntegerv")) return (fn_t)glGetIntegerv;
	fn_t f = real ? (fn_t)dlsym(real, name) : 0;
	if (!f && real_gpa) f = real_gpa(name);
	if (!f && spoof && name[0] == 'g' && name[1] == 'l') f = (fn_t)noop;
	return f;
}
