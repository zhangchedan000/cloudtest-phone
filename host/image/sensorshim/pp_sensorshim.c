// SPDX-License-Identifier: Apache-2.0
/*
 * pp_sensorshim：传感器清单照真机（2026-11-24，学魔云腾：它的机型包录了真机的传感器清单）。
 *
 * 手机里的传感器服务（multihal）加载的子 HAL 是模拟器的 ranchu（镜像里改名成 ...impl.mtk.so，数据由管家喂，踩坑 #59）。
 * 它的传感器表（名字、厂商、量程、精度、功耗、最快 / 最慢频率、FIFO）是编译死的，所有机型都一样。
 * 这个库挂在原来那个文件名上，文件后面（按页对齐）原样接着真正的 ranchu 库：
 *   1. 子 HAL 入口 sensorsHalGetSubHal_2_1 被调用时，用 android_dlopen_ext 从自己这个文件的偏移处加载真正的库；
 *   2. 在它的可写段里找到传感器表（SensorInfo 数组，开机时由静态构造函数建好），按类型改成真机的值；
 *   3. 再调它的入口，返回它的对象（之后的调用全是它自己的，这里不再参与）。
 * 不多一个文件、不改 hals.conf。配置区（"PPSNCFG1" 开头）由管家按机型写好：
 *   o=<真库在本文件里的偏移>\n
 *   s=<类型>|<名字>|<厂商>|<版本>|<量程>|<精度>|<功耗>|<最快间隔微秒>|<最慢间隔微秒>|<FIFO 保留>|<FIFO 最大>\n …
 * 没有 s= 行时：照样加载真库、什么都不改。
 *
 * SensorInfo（android.hardware.sensors@1.0 / 2.1，64 位）：
 *   0 句柄 int32 | 8 名字 hidl_string | 24 厂商 hidl_string | 40 版本 | 44 类型 | 48 类型名 hidl_string
 *   64 量程 f32 | 68 精度 f32 | 72 功耗 f32 | 76 最快间隔 | 80 FIFO 保留 | 84 FIFO 最大 | 88 权限 hidl_string | 104 最慢间隔 | 108 标志
 * hidl_string：指针(8) 长度 u32 是否自己分配 u8。
 */
typedef unsigned long size_t;
typedef unsigned int u32;
typedef unsigned long u64;

extern void *dlsym(void *h, const char *n);
extern void *android_dlopen_ext(const char *name, int flags, const void *info);
extern int open(const char *path, int flags, ...);
extern int close(int fd);
extern float strtof(const char *s, char **end);
extern long strtol(const char *s, char **end, int base);

struct phdr { u32 type, flags; u64 offset, vaddr, paddr, filesz, memsz, align; };
struct dl_phdr_info { u64 addr; const char *name; const struct phdr *phdr; unsigned short phnum; };
extern int dl_iterate_phdr(int (*cb)(struct dl_phdr_info *, size_t, void *), void *data);

#define EXPORT __attribute__((visibility("default")))
#define CFG_SIZE 16384
#define MAX_S 64

__attribute__((used, aligned(16))) volatile char pp_sensorshim_cfg[CFG_SIZE] = "PPSNCFG1";

struct hstr { const char *p; u32 n; unsigned char own; unsigned char pad[3]; };
struct sinfo {
	int handle, pad0; struct hstr name, vendor; int version, type; struct hstr typestr;
	float maxRange, resolution, power; int minDelay; u32 fifoRes, fifoMax; struct hstr perm; int maxDelay; u32 flags;
};
struct want { int type, version, minDelay, maxDelay; u32 fifoRes, fifoMax; float maxRange, resolution, power; const char *name, *vendor; int used; };

static char buf[CFG_SIZE];
static struct want ws[MAX_S];
static int nws;
static long inner_off;
static void *inner;

static size_t slen(const char *s) { size_t n = 0; while (s[n]) n++; return n; }
static int pre(const char *s, const char *p) { while (*p) if (*s++ != *p++) return 0; return 1; }

/* 把 a|b|c 切开：返回下一段开头，当前段结尾写 \0 */
static char *field(char *s) { while (*s && *s != '|') s++; if (*s) *s++ = 0; return s; }

static void parse(void) {
	int i;
	for (i = 0; i < CFG_SIZE - 9; i++) buf[i] = pp_sensorshim_cfg[9 + i];
	buf[CFG_SIZE - 10] = 0;
	char *s = buf;
	while (*s) {
		char *e = s;
		while (*e && *e != '\n') e++;
		if (*e) *e++ = 0;
		if (pre(s, "o=")) inner_off = strtol(s + 2, 0, 10);
		else if (pre(s, "s=") && nws < MAX_S) {
			struct want *w = &ws[nws];
			char *f = s + 2, *n;
			n = field(f); w->type = (int)strtol(f, 0, 10); f = n;
			n = field(f); w->name = f; f = n;
			n = field(f); w->vendor = f; f = n;
			n = field(f); w->version = (int)strtol(f, 0, 10); f = n;
			n = field(f); w->maxRange = strtof(f, 0); f = n;
			n = field(f); w->resolution = strtof(f, 0); f = n;
			n = field(f); w->power = strtof(f, 0); f = n;
			n = field(f); w->minDelay = (int)strtol(f, 0, 10); f = n;
			n = field(f); w->maxDelay = (int)strtol(f, 0, 10); f = n;
			n = field(f); w->fifoRes = (u32)strtol(f, 0, 10); f = n;
			field(f); w->fifoMax = (u32)strtol(f, 0, 10);
			if (w->type > 0 && *w->name) nws++;
		}
		s = e;
	}
}

/* 真库的一个可执行地址，用来在 dl_iterate_phdr 里认出它（按文件加载的库，名字和本库一样） */
static u64 probe_addr;
static u64 rw_lo[8], rw_hi[8];
static int nrw;

static int find_cb(struct dl_phdr_info *info, size_t size, void *data) {
	(void)size; (void)data;
	int i, hit = 0;
	for (i = 0; i < info->phnum; i++) {
		const struct phdr *ph = &info->phdr[i];
		if (ph->type == 1 && probe_addr >= info->addr + ph->vaddr && probe_addr < info->addr + ph->vaddr + ph->memsz) hit = 1;
	}
	if (!hit) return 0;
	for (i = 0; i < info->phnum && nrw < 8; i++) {
		const struct phdr *ph = &info->phdr[i];
		if (ph->type == 1 && (ph->flags & 2)) {
			rw_lo[nrw] = info->addr + ph->vaddr;
			rw_hi[nrw] = info->addr + ph->vaddr + ph->memsz;
			nrw++;
		}
	}
	return 1;
}

/* 一个像样的 hidl_string：长度合理、自己分配标志是 0/1 */
static int okstr(const struct hstr *h, u32 lo, u32 hi) { return h->p && h->n >= lo && h->n <= hi && h->own <= 1; }

static void patch_table(void) {
	int r;
	for (r = 0; r < nrw; r++) {
		u64 a;
		for (a = (rw_lo[r] + 7) & ~7UL; a + sizeof(struct sinfo) <= rw_hi[r]; a += 8) {
			struct sinfo *si = (struct sinfo *)a;
			if (si->type <= 0 || si->type > 100 || si->version < 0 || si->version > 1000) continue;
			if (!okstr(&si->typestr, 15, 80) || !okstr(&si->name, 1, 200) || !okstr(&si->vendor, 1, 200)) continue;
			if (!pre(si->typestr.p, "android.sensor.")) continue;
			int k;
			for (k = 0; k < nws; k++) {
				struct want *w = &ws[k];
				if (w->used || w->type != si->type) continue;
				w->used = 1;
				si->name.p = w->name; si->name.n = (u32)slen(w->name); si->name.own = 0;
				si->vendor.p = w->vendor; si->vendor.n = (u32)slen(w->vendor); si->vendor.own = 0;
				si->version = w->version;
				if (w->maxRange > 0) si->maxRange = w->maxRange;
				if (w->resolution > 0) si->resolution = w->resolution;
				if (w->power > 0) si->power = w->power;
				/* 变化型（距离、光线……）和一次性的最快间隔真机是 0 / -1，跟着真机；连续型不能比 HAL 能给的还快，交给真机值 */
				si->minDelay = w->minDelay;
				si->maxDelay = w->maxDelay;
				si->fifoRes = w->fifoRes;
				si->fifoMax = w->fifoMax;
				break;
			}
			a += sizeof(struct sinfo) - 8;
		}
	}
}

struct dlext { u64 flags; void *reserved_addr; size_t reserved_size; int relro_fd; int library_fd; long library_fd_offset; void *ns; };
#define DLEXT_USE_LIBRARY_FD 0x10
#define DLEXT_USE_LIBRARY_FD_OFFSET 0x20

static const char *self_name;
static int self_cb(struct dl_phdr_info *info, size_t size, void *data) {
	(void)size; (void)data;
	int i;
	for (i = 0; i < info->phnum; i++) {
		const struct phdr *ph = &info->phdr[i];
		if (ph->type == 1 && probe_addr >= info->addr + ph->vaddr && probe_addr < info->addr + ph->vaddr + ph->memsz) {
			self_name = info->name;
			return 1;
		}
	}
	return 0;
}

EXPORT void *sensorsHalGetSubHal_2_1(u32 *version) {
	static void *(*entry)(u32 *);
	if (!inner) {
		parse();
		probe_addr = (u64)(void *)sensorsHalGetSubHal_2_1;
		dl_iterate_phdr(self_cb, 0);
		if (!self_name || inner_off <= 0) return 0;
		int fd = open(self_name, 0x80000 /* O_CLOEXEC */);
		if (fd < 0) return 0;
		struct dlext ext = { DLEXT_USE_LIBRARY_FD | DLEXT_USE_LIBRARY_FD_OFFSET, 0, 0, -1, fd, inner_off, 0 };
		inner = android_dlopen_ext("libpp_sensors_inner.so", 2 /* RTLD_NOW */, &ext);
		close(fd);
		if (!inner) return 0;
		entry = (void *(*)(u32 *))dlsym(inner, "sensorsHalGetSubHal_2_1");
		if (!entry) return 0;
		if (nws > 0) {
			probe_addr = (u64)(void *)entry;
			nrw = 0;
			dl_iterate_phdr(find_cb, 0);
			patch_table();
		}
	}
	return entry ? entry(version) : 0;
}
