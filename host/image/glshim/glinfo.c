// 测试用：手机里建一个 GL 上下文，打印 App 能拿到的显卡字符串（验证伪装档的转接库）。adb shell 里跑：/data/local/tmp/pp-glinfo
typedef void *EGLDisplay, *EGLConfig, *EGLContext, *EGLSurface;
typedef int EGLint;
typedef unsigned int GLenum;
extern EGLDisplay eglGetDisplay(void *);
extern unsigned eglInitialize(EGLDisplay, EGLint *, EGLint *);
extern unsigned eglChooseConfig(EGLDisplay, const EGLint *, EGLConfig *, EGLint, EGLint *);
extern EGLContext eglCreateContext(EGLDisplay, EGLConfig, EGLContext, const EGLint *);
extern EGLSurface eglCreatePbufferSurface(EGLDisplay, EGLConfig, const EGLint *);
extern unsigned eglMakeCurrent(EGLDisplay, EGLSurface, EGLSurface, EGLContext);
extern const char *glGetString(GLenum);
extern const char *glGetStringi(GLenum, unsigned);
extern void glGetIntegerv(GLenum, int *);
extern int printf(const char *, ...);
extern void exit(int);
static int run(void) {
	EGLDisplay d = eglGetDisplay(0);
	eglInitialize(d, 0, 0);
	EGLint ca[] = {0x3040 /*RENDERABLE_TYPE*/, 0x40 /*ES3*/, 0x3033 /*SURFACE_TYPE*/, 1 /*PBUFFER*/, 0x3038};
	EGLConfig c; EGLint n = 0;
	if (!eglChooseConfig(d, ca, &c, 1, &n) || n < 1) { printf("no config\n"); return 1; }
	EGLint sa[] = {0x3057, 16, 0x3056, 16, 0x3038};
	EGLSurface s = eglCreatePbufferSurface(d, c, sa);
	EGLint xa[] = {0x3098 /*CONTEXT_CLIENT_VERSION*/, 3, 0x3038};
	EGLContext x = eglCreateContext(d, c, 0, xa);
	if (!x || !eglMakeCurrent(d, s, s, x)) { printf("no context\n"); return 1; }
	int ne = 0; glGetIntegerv(0x821D, &ne);
	const char *e = glGetString(0x1F03); int len = 0; while (e && e[len]) len++;
	printf("vendor=%s\nrenderer=%s\nversion=%s\nglsl=%s\nnum_ext=%d\next_len=%d\next0=%s\n", glGetString(0x1F00), glGetString(0x1F01),
	       glGetString(0x1F02), glGetString(0x8B8C), ne, len, ne > 0 ? glGetStringi(0x1F03, 0) : "");
	return 0;
}

/* 不链 C 运行时（没有 NDK）：入口自己写，跑完 exit（libc 由动态链接器先初始化好） */
void _start(void) { exit(run()); }
