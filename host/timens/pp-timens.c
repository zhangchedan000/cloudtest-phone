// pp-timens：单线程的小启动器，让安卓进到自己的时间命名空间（开机时长从 0 开始，踩坑 #49 / #132）。
//
// 为什么要它：unshare(CLONE_NEWTIME) 只把「以后的子进程」放进新命名空间，调用者自己还在旧的里面；内核规定这时
// 不许再建线程（clone CLONE_VM/CLONE_THREAD 返回 EINVAL）。主线内核在 exec 时会把进程切进新命名空间，
// 魔云腾 Q1 的 6.1.141 内核不会——exec 出来的安卓 init 一建线程就失败、开不了机（踩坑 #98）。
// setns() 进时间命名空间会把「自己」和「子进程」一起切过去，但要求调用者是单线程；管家是 Go 程序（天生多线程），
// 所以由它 exec 这个不依赖任何库的小程序：unshare → 写偏移 → setns 自己进去 → exec 安卓 init。
//
// 用法：pp-timens [--selinux=permissive|enforcing] <开机时长秒数> <程序> [参数...]
// 任何一步失败都照样 exec 程序（开机时长保持宿主机的，绝不因此开不了机）。
//
// --selinux（踩坑 #135）：先给这台手机加载安卓自己的 SELinux 规则，再启动安卓。
// 容器版安卓的 init 跳过了加载规则这一步（手机里 getenforce = Disabled）；魔云腾 Q1 内核带 SELinux 命名空间，
// 容器里挂 selinuxfs 就是这台手机自己的一套，加载规则、开强制模式都不影响宿主机和别的手机。做的事和安卓
// init 的 selinux_setup 阶段一样：挂 selinuxfs → 把 /vendor/etc/selinux/precompiled_sepolicy 写进 load →
// 设模式 → 下一次 exec 进 u:r:init:s0。失败就照旧不开 SELinux 启动。
// 编译（arm64，不链接 libc）：sh host/timens/build.sh

#define SYS_mount 40
#define SYS_openat 56
#define SYS_fstat 80
#define SYS_munmap 215
#define SYS_mmap 222
#define SYS_close 57
#define SYS_write 64
#define SYS_exit 93
#define SYS_unshare 97
#define SYS_clock_gettime 113
#define SYS_execve 221
#define SYS_setns 268
#define AT_FDCWD (-100)
#define O_WRONLY 1
#define O_CLOEXEC 02000000
#define CLONE_NEWTIME 0x80
#define PROT_READ 1
#define MAP_PRIVATE 2

static long sc(long n, long a, long b, long c, long d) {
	register long x8 __asm__("x8") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	register long x3 __asm__("x3") = d;
	__asm__ volatile("svc 0" : "+r"(x0) : "r"(x8), "r"(x1), "r"(x2), "r"(x3) : "memory");
	return x0;
}

struct ts { long sec, nsec; };

static long now(int clock) {
	struct ts t = {0, 0};
	sc(SYS_clock_gettime, clock, (long)&t, 0, 0);
	return t.sec;
}

static int slen(const char *s) { int n = 0; while (s[n]) n++; return n; }

static long atol10(const char *s) {
	long v = 0;
	while (*s >= '0' && *s <= '9') v = v * 10 + (*s++ - '0');
	return v;
}

// 把有符号整数写进 buf，返回写了几个字节
static int itoa10(long v, char *buf) {
	char tmp[24];
	int n = 0, neg = v < 0;
	unsigned long u = neg ? -(unsigned long)v : (unsigned long)v;
	do { tmp[n++] = '0' + u % 10; u /= 10; } while (u);
	int k = 0;
	if (neg) buf[k++] = '-';
	while (n) buf[k++] = tmp[--n];
	return k;
}

static int append(char *dst, int at, const char *s) {
	for (int i = 0; s[i]; i++) dst[at++] = s[i];
	return at;
}

static void say(const char *m) { sc(SYS_write, 2, (long)m, slen(m), 0); }

static long sc6(long n, long a, long b, long c, long d, long e, long f) {
	register long x8 __asm__("x8") = n;
	register long x0 __asm__("x0") = a;
	register long x1 __asm__("x1") = b;
	register long x2 __asm__("x2") = c;
	register long x3 __asm__("x3") = d;
	register long x4 __asm__("x4") = e;
	register long x5 __asm__("x5") = f;
	__asm__ volatile("svc 0" : "+r"(x0) : "r"(x8), "r"(x1), "r"(x2), "r"(x3), "r"(x4), "r"(x5) : "memory");
	return x0;
}

static int streq(const char *a, const char *b) {
	while (*a && *a == *b) a++, b++;
	return *a == *b;
}

// 往一个文件里写一串（整串写完返回 0）
static int put(const char *path, const char *buf, long n) {
	long fd = sc(SYS_openat, AT_FDCWD, (long)path, O_WRONLY | O_CLOEXEC, 0);
	if (fd < 0) return -1;
	long w = sc(SYS_write, fd, (long)buf, n, 0);
	sc(SYS_close, fd, 0, 0, 0);
	return w == n ? 0 : -1;
}

// 加载这台手机自己的 SELinux 规则。返回 0 = 成功（之后 exec 会进 init 域）
static int selinux_setup(int enforcing) {
	long r = sc6(SYS_mount, (long)"selinuxfs", (long)"/sys/fs/selinux", (long)"selinuxfs", 0, 0, 0);
	if (r != 0 && r != -16 /* EBUSY：已经挂了 */) { say("pp-timens: mount selinuxfs failed\n"); return -1; }
	long fd = sc(SYS_openat, AT_FDCWD, (long)"/vendor/etc/selinux/precompiled_sepolicy", O_CLOEXEC, 0);
	if (fd < 0) { say("pp-timens: no precompiled_sepolicy\n"); return -1; }
	long st[16];  // struct stat（arm64 128 字节），st_size 在偏移 48
	if (sc(SYS_fstat, fd, (long)st, 0, 0) != 0 || st[6] <= 0) { sc(SYS_close, fd, 0, 0, 0); return -1; }
	long size = st[6];
	long p = sc6(SYS_mmap, 0, size, PROT_READ, MAP_PRIVATE, fd, 0);
	sc(SYS_close, fd, 0, 0, 0);
	if (p < 0 && p > -4096) { say("pp-timens: mmap policy failed\n"); return -1; }
	r = put("/sys/fs/selinux/load", (const char *)p, size);
	sc(SYS_munmap, p, size, 0, 0);
	if (r != 0) { say("pp-timens: load policy failed\n"); return -1; }
	if (put("/sys/fs/selinux/enforce", enforcing ? "1" : "0", 1) != 0) say("pp-timens: set enforce failed\n");
	// 下一次 exec（安卓 init）进 init 域，和 selinux_setup 阶段 setexeccon 一样
	if (put("/proc/self/attr/exec", "u:r:init:s0", 11) != 0) say("pp-timens: setexeccon init failed\n");
	return 0;
}

void cmain(long *sp) {
	long argc = sp[0];
	char **argv = (char **)(sp + 1);
	char **envp = argv + argc + 1;
	int se = -1;  // -1 不动 SELinux，0 只记录不拦截，1 强制
	if (argc > 1 && streq(argv[1], "--selinux=permissive")) se = 0;
	if (argc > 1 && streq(argv[1], "--selinux=enforcing")) se = 1;
	if (se >= 0 || (argc > 1 && argv[1][0] == '-' && argv[1][1] == '-')) {
		argv[1] = argv[0];  // 去掉这个选项
		argv++;
		argc--;
	}
	if (argc < 3) {
		say("usage: pp-timens <uptime-seconds> <program> [args...]\n");
		sc(SYS_exit, 2, 0, 0, 0);
	}
	long uptime = atol10(argv[1]);
	// 先拿住原来的时间命名空间：后面 setns 失败时退回去，免得卡在「子进程在新的、自己在旧的」——那样安卓建不了线程
	long old = sc(SYS_openat, AT_FDCWD, (long)"/proc/self/ns/time", O_CLOEXEC, 0);
	if (old >= 0 && sc(SYS_unshare, CLONE_NEWTIME, 0, 0, 0) == 0) {
		// 偏移 = 想要的开机时长 - 现在的值（CLOCK_MONOTONIC=1，CLOCK_BOOTTIME=7）
		char off[96];
		int n = 0;
		n = append(off, n, "monotonic ");
		n += itoa10(uptime - now(1), off + n);
		n = append(off, n, " 0\nboottime ");
		n += itoa10(uptime - now(7), off + n);
		n = append(off, n, " 0\n");
		long fd = sc(SYS_openat, AT_FDCWD, (long)"/proc/self/timens_offsets", O_WRONLY | O_CLOEXEC, 0);
		if (fd < 0 || sc(SYS_write, fd, (long)off, n, 0) != n) say("pp-timens: write timens_offsets failed\n");
		if (fd >= 0) sc(SYS_close, fd, 0, 0, 0);
		// 自己也进去（单线程才允许）：之后 exec 的安卓 init 和它建的线程都在新命名空间里
		long ns = sc(SYS_openat, AT_FDCWD, (long)"/proc/self/ns/time_for_children", O_CLOEXEC, 0);
		if (ns < 0 || sc(SYS_setns, ns, CLONE_NEWTIME, 0, 0) != 0) {
			say("pp-timens: setns(time) failed, back to host uptime\n");
			sc(SYS_setns, old, CLONE_NEWTIME, 0, 0);
		}
		if (ns >= 0) sc(SYS_close, ns, 0, 0, 0);
	} else {
		say("pp-timens: unshare(CLONE_NEWTIME) failed, keeping host uptime\n");
	}
	if (old >= 0) sc(SYS_close, old, 0, 0, 0);
	// SELinux 放在最后（时间命名空间那几步用的 /proc 文件不受规则影响）
	if (se >= 0 && selinux_setup(se) != 0) say("pp-timens: SELinux not loaded, booting without it\n");
	sc(SYS_execve, (long)argv[2], (long)(argv + 2), (long)envp, 0);
	say("pp-timens: execve failed\n");
	sc(SYS_exit, 1, 0, 0, 0);
}

// 入口用纯汇编写（arm64 的 GCC 不认 naked，C 函数会先动 sp，拿不到内核放在栈顶的 argc/argv）
__asm__(".text\n.global _start\n.type _start, %function\n_start:\n\tmov x0, sp\n\tbl cmain\n\tbrk #0\n");
