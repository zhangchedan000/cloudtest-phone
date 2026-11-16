// libppclock.so：只给 wpa_supplicant 用的时钟垫片（踩坑 #57）。
// 手机活在时间命名空间里（开机时长归零，#49），CLOCK_BOOTTIME 比宿主机小一个偏移；
// 但内核给扫描结果打的时间戳（NL80211_BSS_LAST_SEEN_BOOTTIME）是宿主机的真实 BOOTTIME，
// wpa_supplicant 一比较就把所有热点当成「几个月前的旧结果」跳过（skip - scan result not recent enough），
// WiFi 永远连不上。这里拦下 clock_gettime：BOOTTIME 类时钟把命名空间偏移加回去，
// 让 wpa_supplicant 和内核看到同一个时钟。不依赖 libc（直接系统调用），
// 通过 init rc 的 setenv LD_PRELOAD 只加给 wpa_supplicant，其他进程不受影响。
// 编译（ARM64 宿主机）：gcc -shared -fPIC -nostdlib -O2 -Wl,-z,max-page-size=16384 -Wl,--hash-style=both -Wl,-soname,libppclock.so -o libppclock.so ppclock.c
typedef long time_t_;
struct ts_ { time_t_ sec; long nsec; };

static long sys3(long n, long a, long b, long c) {
    register long x8 __asm__("x8") = n;
    register long x0 __asm__("x0") = a;
    register long x1 __asm__("x1") = b;
    register long x2 __asm__("x2") = c;
    __asm__ volatile("svc #0" : "+r"(x0) : "r"(x8), "r"(x1), "r"(x2) : "memory");
    return x0;
}

static int loaded;
static long boot_sec, boot_nsec;

static void load_offset(void) {
    // /proc/self/timens_offsets：每行 "monotonic <秒> <纳秒>" / "boottime <秒> <纳秒>"
    char buf[256];
    long fd = sys3(56 /*openat*/, -100 /*AT_FDCWD*/, (long)"/proc/self/timens_offsets", 0);
    loaded = 1;
    if (fd < 0) return;
    long n = sys3(63 /*read*/, fd, (long)buf, sizeof(buf) - 1);
    sys3(57 /*close*/, fd, 0, 0);
    if (n <= 0) return;
    buf[n] = 0;
    for (char *p = buf; *p; ) {
        if (p[0] == 'b' && p[1] == 'o' && p[2] == 'o' && p[3] == 't') {
            while (*p && *p != ' ') p++;
            while (*p == ' ') p++;
            long sign = 1, v = 0;
            if (*p == '-') { sign = -1; p++; }
            while (*p >= '0' && *p <= '9') v = v * 10 + (*p++ - '0');
            boot_sec = sign * v;
            while (*p == ' ') p++;
            v = 0; while (*p >= '0' && *p <= '9') v = v * 10 + (*p++ - '0');
            boot_nsec = v;
            return;
        }
        while (*p && *p != '\n') p++;
        if (*p) p++;
    }
}

int clock_gettime(int clk, struct ts_ *ts) {
    long r = sys3(113 /*clock_gettime*/, clk, (long)ts, 0);
    if (r != 0) return -1;
    if (clk == 7 /*CLOCK_BOOTTIME*/ || clk == 9 /*CLOCK_BOOTTIME_ALARM*/) {
        if (!loaded) load_offset();
        ts->sec -= boot_sec;
        ts->nsec -= boot_nsec;
        if (ts->nsec < 0) { ts->nsec += 1000000000L; ts->sec--; }
    }
    return 0;
}
