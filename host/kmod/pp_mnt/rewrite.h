/* SPDX-License-Identifier: GPL-2.0 */
/*
 * pp_mnt 的改写规则（纯 C，内核模块和用户态测试程序共用，踩坑 #68）。
 *
 * 输入一行 /proc/<pid>/mountinfo（mi=1）或 /proc/<pid>/mounts（mi=0）的输出（带结尾 \n），
 * 输出改写后的一行。返回值：-1 原样不动；0 删掉这一行；>0 新的一行的长度（写在 out 里）。
 *
 * 云手机里能看出是容器的挂载：
 *   - 根目录和 /apex/...、/etc 是 Docker 的 overlay（lowerdir=/var/lib/containerd/...）→ 改成真机的样子
 *     /dev/block/dm-0 ext4 ro（真机系统分区是只读 ext4 / erofs，设备号 253:x）；
 *   - /data 是宿主机目录 bind 进来的（mountinfo 第 4 列是宿主机路径 /opt/prism-phone/data/phones/<id>/data/...）
 *     → 去掉宿主机前缀，设备改成 /dev/block/dm-34；
 *   - Docker 塞的 /etc/resolv.conf、hostname、hosts，管家塞的 build.prop、.empty（遮 /proc/vmallocinfo 等），
 *     小助手绑的伪终端（/dev/goldfish_pipe*、/dev/ttyC0），mqueue、/dev/shm（安卓没有）→ 删掉；
 *     判断办法：来源是宿主机磁盘（/dev/ 开头、不是 /dev/block/）、但不是这台手机 /data 的，一律删。
 * cfg 由管家从这台手机自己的挂载表里读出来：/data 那一行的来源（/dev/sda1 或磁盘镜像的 /dev/loopN）和 root
 * （宿主机目录，或磁盘镜像时是 /）。
 * 宿主机自己的进程不受影响（模块只改登记过的手机 PID 命名空间里的进程读到的内容）。
 */
#ifndef PP_MNT_REWRITE_H
#define PP_MNT_REWRITE_H

#define PP_PREFIX_MAX 256
#define PP_SRC_MAX 64
#define PP_MAX_TOK 40

struct pp_cfg {
	char prefix[PP_PREFIX_MAX]; /* 这台手机 /data 在那块盘上的路径，如 /opt/prism-phone/data/phones/<id>/data；磁盘镜像是 / */
	char src[PP_SRC_MAX];       /* /data 那块盘在挂载表里的名字，如 /dev/sda1、/dev/loop5 */
};

struct pp_tok {
	const char *p;
	int n;
};

static int pp_split(const char *s, int len, struct pp_tok *t, int max)
{
	int i = 0, k = 0;

	while (i < len && k < max) {
		while (i < len && (s[i] == ' ' || s[i] == '\n'))
			i++;
		if (i >= len)
			break;
		t[k].p = s + i;
		while (i < len && s[i] != ' ' && s[i] != '\n')
			i++;
		t[k].n = (int)(t[k].p ? (s + i) - t[k].p : 0);
		k++;
	}
	return k;
}

static int pp_eq(struct pp_tok t, const char *s)
{
	int n = (int)strlen(s);

	return t.n == n && memcmp(t.p, s, n) == 0;
}

static int pp_starts(struct pp_tok t, const char *s)
{
	int n = (int)strlen(s);

	return t.n >= n && memcmp(t.p, s, n) == 0;
}

static int pp_ends(struct pp_tok t, const char *s)
{
	int n = (int)strlen(s);

	return t.n >= n && memcmp(t.p + t.n - n, s, n) == 0;
}

static int pp_contains(struct pp_tok t, const char *s)
{
	int n = (int)strlen(s), i;

	for (i = 0; i + n <= t.n; i++)
		if (memcmp(t.p + i, s, n) == 0)
			return 1;
	return 0;
}

/* 路径 t 在目录 prefix 下（等于它或以 prefix/ 开头）：返回剩下部分的长度（0 = 正好是它），不在返回 -1 */
static int pp_under(struct pp_tok t, const char *prefix)
{
	int n = (int)strlen(prefix);

	if (n == 1 && prefix[0] == '/')
		return t.n; /* /data 是整块盘（磁盘镜像）：root 原样 */
	if (n == 0 || t.n < n || memcmp(t.p, prefix, n) != 0)
		return -1;
	if (t.n == n)
		return 0;
	if (t.p[n] != '/')
		return -1;
	return t.n - n;
}

/* 输出缓冲 */
struct pp_out {
	char *b;
	int n, max, bad;
};

static void pp_put(struct pp_out *o, const char *s, int n)
{
	if (o->bad || o->n + n > o->max) {
		o->bad = 1;
		return;
	}
	memcpy(o->b + o->n, s, n);
	o->n += n;
}

static void pp_puts(struct pp_out *o, const char *s)
{
	pp_put(o, s, (int)strlen(s));
}

static void pp_putt(struct pp_out *o, struct pp_tok t)
{
	pp_put(o, t.p, t.n);
}

/* 来源是宿主机的块设备（/dev/sda1、/dev/loop3、/dev/nvme0n1p2…，容器里不该有）：真机的是 /dev/block/... */
static int pp_host_dev(struct pp_tok src)
{
	return pp_starts(src, "/dev/") && !pp_starts(src, "/dev/block/") && !pp_eq(src, "/dev/fuse"); /* 真机 /storage 也是 /dev/fuse */
}

/* 不管哪种格式都删的：mqueue、lxcfs（遮 /proc/meminfo 等）、/dev/shm、不在 /dev/pts 的 devpts（小助手绑的伪终端）、Docker 塞的 /etc 文件 */
static int pp_always_drop(struct pp_tok fstype, struct pp_tok mnt)
{
	return pp_eq(fstype, "mqueue") || pp_eq(fstype, "fuse.lxcfs") || pp_eq(mnt, "/dev/shm") ||
	       (pp_eq(fstype, "devpts") && !pp_eq(mnt, "/dev/pts")) ||
	       pp_eq(mnt, "/etc/resolv.conf") || pp_eq(mnt, "/etc/hostname") || pp_eq(mnt, "/etc/hosts");
}

#define PP_SYS_DEV "/dev/block/dm-0"
#define PP_SYS_MAJMIN "253:0"
#define PP_SYS_OPTS "ro,seclabel,relatime"
#define PP_DATA_DEV "/dev/block/dm-34"
#define PP_DATA_MAJMIN "253:34"
#define PP_DATA_OPTS "rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic"

static int pp_rewrite_mountinfo(const char *in, int len, char *out, int max, const struct pp_cfg *c)
{
	struct pp_tok t[PP_MAX_TOK];
	struct pp_out o = { out, 0, max, 0 };
	int k = pp_split(in, len, t, PP_MAX_TOK), dash = -1, i, rest;

	for (i = 6; i < k; i++)
		if (pp_eq(t[i], "-")) {
			dash = i;
			break;
		}
	if (k < 7 || dash < 0 || dash + 3 > k)
		return -1;
	/* t[3]=root t[4]=挂载点 t[5]=挂载选项 t[6..dash)=可选字段 t[dash+1]=类型 t[dash+2]=来源 t[dash+3]=超级块选项 */
	if (pp_always_drop(t[dash + 1], t[4]))
		return 0;
	if (pp_starts(t[3], "/var/lib/docker/") || pp_starts(t[3], "/var/lib/containerd/") ||
	    pp_contains(t[3], "/buildprop/") || pp_ends(t[3], "/.empty"))
		return 0;

	if (pp_eq(t[dash + 1], "overlay")) {
		pp_putt(&o, t[0]); pp_puts(&o, " "); pp_putt(&o, t[1]); pp_puts(&o, " " PP_SYS_MAJMIN " ");
		pp_putt(&o, t[3]); pp_puts(&o, " "); pp_putt(&o, t[4]); pp_puts(&o, " ro,relatime");
		for (i = 6; i < dash; i++) {
			pp_puts(&o, " ");
			pp_putt(&o, t[i]);
		}
		pp_puts(&o, " - ext4 " PP_SYS_DEV " " PP_SYS_OPTS "\n");
		return o.bad ? -1 : o.n;
	}

	if (c->src[0] && pp_eq(t[dash + 2], c->src)) {
		rest = pp_under(t[3], c->prefix);
		if (rest < 0)
			return 0; /* 宿主机同一块盘上、不是这台手机 /data 的 bind：删 */
		pp_putt(&o, t[0]); pp_puts(&o, " "); pp_putt(&o, t[1]); pp_puts(&o, " " PP_DATA_MAJMIN " ");
		if (rest == 0)
			pp_puts(&o, "/");
		else
			pp_put(&o, t[3].p + (t[3].n - rest), rest);
		pp_puts(&o, " "); pp_putt(&o, t[4]); pp_puts(&o, " ");
		pp_putt(&o, t[5]);
		for (i = 6; i < dash; i++) {
			pp_puts(&o, " ");
			pp_putt(&o, t[i]);
		}
		pp_puts(&o, " - ");
		pp_putt(&o, t[dash + 1]);
		pp_puts(&o, " " PP_DATA_DEV " " PP_DATA_OPTS "\n");
		return o.bad ? -1 : o.n;
	}
	if (pp_host_dev(t[dash + 2]))
		return 0; /* 宿主机别的盘 bind 进来的（build.prop 等） */
	return -1;
}

/* /proc/mounts 和 mountstats 没有 root 列，只能按「来源 + 挂载点 + 类型」判断 */
enum { PP_KEEP, PP_DROP, PP_SYS, PP_DATA };

static int pp_classify(struct pp_tok src, struct pp_tok mnt, struct pp_tok fstype, const struct pp_cfg *c)
{
	if (pp_always_drop(fstype, mnt))
		return PP_DROP;
	if (pp_eq(fstype, "overlay"))
		return PP_SYS;
	if (c->src[0] && pp_eq(src, c->src)) {
		/* 同一块盘上挂到系统目录的是管家伪造的文件（cpuinfo、build.prop…）：删；其余是 /data 的各种 bind（/storage 等）：改 */
		if (pp_starts(mnt, "/proc/") || pp_starts(mnt, "/sys/") || pp_starts(mnt, "/system/") ||
		    pp_starts(mnt, "/etc/") || pp_starts(mnt, "/vendor/") || pp_starts(mnt, "/dev/") ||
		    pp_starts(mnt, "/product/") || pp_starts(mnt, "/odm/"))
			return PP_DROP;
		return PP_DATA;
	}
	if (pp_host_dev(src))
		return PP_DROP;
	return PP_KEEP;
}

/* /proc/mounts：来源 挂载点 类型 选项 0 0 */
static int pp_rewrite_mounts(const char *in, int len, char *out, int max, const struct pp_cfg *c)
{
	struct pp_tok t[8];
	struct pp_out o = { out, 0, max, 0 };
	int k = pp_split(in, len, t, 8);

	if (k < 4)
		return -1;
	switch (pp_classify(t[0], t[1], t[2], c)) {
	case PP_DROP:
		return 0;
	case PP_SYS:
		pp_puts(&o, PP_SYS_DEV " ");
		pp_putt(&o, t[1]);
		pp_puts(&o, " ext4 " PP_SYS_OPTS " 0 0\n");
		return o.bad ? -1 : o.n;
	case PP_DATA:
		pp_puts(&o, PP_DATA_DEV " ");
		pp_putt(&o, t[1]);
		pp_puts(&o, " ");
		pp_putt(&o, t[2]);
		pp_puts(&o, " " PP_DATA_OPTS " 0 0\n");
		return o.bad ? -1 : o.n;
	}
	return -1;
}

/* /proc/<pid>/mountstats：device 来源 mounted on 挂载点 with fstype 类型[ 统计…]（nfs 等会多几行，这里只有本地盘） */
static int pp_rewrite_mountstats(const char *in, int len, char *out, int max, const struct pp_cfg *c)
{
	struct pp_tok t[8];
	struct pp_out o = { out, 0, max, 0 };
	int k = pp_split(in, len, t, 8), cls;

	if (k < 8 || !pp_eq(t[0], "device") || !pp_eq(t[2], "mounted") || !pp_eq(t[5], "with") || !pp_eq(t[6], "fstype"))
		return -1;
	cls = pp_classify(t[1], t[4], t[7], c);
	if (cls == PP_DROP)
		return 0;
	if (cls == PP_KEEP)
		return -1;
	pp_puts(&o, "device ");
	pp_puts(&o, cls == PP_SYS ? PP_SYS_DEV : PP_DATA_DEV);
	pp_puts(&o, " mounted on ");
	pp_putt(&o, t[4]);
	pp_puts(&o, " with fstype ");
	if (cls == PP_SYS)
		pp_puts(&o, "ext4");
	else
		pp_putt(&o, t[7]);
	pp_puts(&o, "\n");
	return o.bad ? -1 : o.n;
}

/*
 * /proc/net/tcp、tcp6、udp、udp6 的一行：「  序号: 本地地址:端口 远端地址:端口 状态 …」（十六进制）。
 * 删掉云手机自己用的本机端口（踩坑 #77）：6401~6404 蓝牙芯片模拟器、9600 模拟基带；
 * 以及 Docker 的内置 DNS（本地地址 127.0.0.11，四字节小端写成 0B00007F）——真手机没有，看到就知道是 Docker。
 */
static int pp_hex(struct pp_tok t, unsigned long *v)
{
	int i;

	*v = 0;
	if (t.n == 0 || t.n > 16)
		return -1;
	for (i = 0; i < t.n; i++) {
		char c = t.p[i];

		*v <<= 4;
		if (c >= '0' && c <= '9')
			*v |= c - '0';
		else if (c >= 'A' && c <= 'F')
			*v |= c - 'A' + 10;
		else if (c >= 'a' && c <= 'f')
			*v |= c - 'a' + 10;
		else
			return -1;
	}
	return 0;
}

static int pp_rewrite_net(const char *in, int len)
{
	struct pp_tok t[4], addr, port;
	unsigned long p;
	int k = pp_split(in, len, t, 4), i;

	if (k < 2 || !pp_ends(t[0], ":"))
		return -1; /* 表头那行（sl local_address …）原样 */
	for (i = t[1].n - 1; i >= 0 && t[1].p[i] != ':'; i--)
		;
	if (i <= 0)
		return -1;
	addr.p = t[1].p; addr.n = i;
	port.p = t[1].p + i + 1; port.n = t[1].n - i - 1;
	if (pp_hex(port, &p))
		return -1;
	if ((p >= 6401 && p <= 6404) || p == 9600)
		return 0;
	if (pp_eq(addr, "0B00007F") || pp_eq(addr, "0000000000000000FFFF00000B00007F"))
		return 0;
	return -1;
}

/* 在 h[0..hn) 里找子串 n（内核里没有 memmem，自己写一个短的）。 */
static int pp_mem(const char *h, int hn, const char *n)
{
	int nn = 0, i, j;

	while (n[nn])
		nn++;
	for (i = 0; i + nn <= hn; i++) {
		for (j = 0; j < nn && h[i + j] == n[j]; j++)
			;
		if (j == nn)
			return 1;
	}
	return 0;
}

/*
 * /proc/bus/input/devices（踩坑 #82）：每次 input_devices_seq_show 输出一块输入设备信息。
 * 名字带 QEMU / virtio / VMware / VirtualBox / goldfish 的是虚拟机/模拟器的输入设备——真机里
 * 不该有「QEMU QEMU USB Tablet/Keyboard/Mouse」这种，整块删掉；触摸屏 fts_ts、电源键等原样保留。
 */
static int pp_rewrite_input(const char *in, int len)
{
	if (pp_mem(in, len, "QEMU") || pp_mem(in, len, "virtio") ||
	    pp_mem(in, len, "VMware") || pp_mem(in, len, "VirtualBox") ||
	    pp_mem(in, len, "Goldfish") || pp_mem(in, len, "goldfish"))
		return 0; /* 整块删掉 */
	return -1; /* 其余原样 */
}

/*
 * /proc/uptime：「<开机时长> <空闲时间>\n」，两个十进制、各两位小数（秒）。
 * 开机时长（第一个数）已被时间命名空间按每台手机调过（对）；空闲时间（第二个数）还是宿主机累计的——
 * 几台手机一模一样、而且远大于本机开机时长（宿主机开了好多天），一看就不对（踩坑 #90）。
 * 改成 ≈ 本机开机时长 × 在线 CPU 数 × 0.98：像一台刚开机不久、基本空闲的真机，每台跟着自己的开机时长走。
 */
static int pp_parse_cs(struct pp_tok t, unsigned long long *cs)
{
	unsigned long long sec = 0;
	int i = 0, frac = 0, nf = 0;

	if (t.n == 0)
		return -1;
	for (; i < t.n && t.p[i] != '.'; i++) {
		if (t.p[i] < '0' || t.p[i] > '9')
			return -1;
		sec = sec * 10 + (unsigned)(t.p[i] - '0');
	}
	if (i < t.n) {
		i++;
		for (; i < t.n && nf < 2; i++, nf++) {
			if (t.p[i] < '0' || t.p[i] > '9')
				return -1;
			frac = frac * 10 + (t.p[i] - '0');
		}
	}
	while (nf < 2) {
		frac *= 10;
		nf++;
	}
	*cs = sec * 100ULL + (unsigned)frac;
	return 0;
}

static int pp_fmt_cs(unsigned long long cs, char *buf)
{
	unsigned long long sec = cs / 100;
	int frac = (int)(cs % 100), n = 0, j, s;
	char tmp[24];

	if (sec == 0)
		tmp[n++] = '0';
	else
		for (; sec > 0; sec /= 10)
			tmp[n++] = '0' + (int)(sec % 10);
	for (j = 0, s = n - 1; s >= 0; s--, j++)
		buf[j] = tmp[s];
	buf[j++] = '.';
	buf[j++] = '0' + frac / 10;
	buf[j++] = '0' + frac % 10;
	return j;
}

static int pp_rewrite_uptime(const char *in, int len, char *out, int max, int ncpu)
{
	struct pp_tok t[4];
	struct pp_out o = { out, 0, max, 0 };
	int k = pp_split(in, len, t, 4), bn;
	unsigned long long up_cs, idle_cs;
	char buf[24];

	if (k < 2 || ncpu < 1)
		return -1;
	if (pp_parse_cs(t[0], &up_cs))
		return -1;
	idle_cs = up_cs * (unsigned long long)ncpu * 98ULL / 100ULL;
	pp_putt(&o, t[0]);
	pp_puts(&o, " ");
	bn = pp_fmt_cs(idle_cs, buf);
	pp_put(&o, buf, bn);
	pp_puts(&o, "\n");
	return o.bad ? -1 : o.n;
}

/*
 * /proc/stat 的 cpu / cpuN 行是宿主机从开机到现在的累计时间（USER_HZ=100 的 tick），几台手机一模一样、
 * 而且和本机开机时长完全不符（母机级相关性，踩坑 #94）。按本机开机时长 up_sec 重算：每核总 tick=up_sec*100，
 * 基本空闲（idle 97%、system 1.5%、user 补余）；聚合 cpu 行 ×核数。btime、intr、ctxt 等其余行原样。
 */
static int pp_fmt_u64(unsigned long long v, char *buf)
{
	char tmp[24];
	int n = 0, j, s;

	if (v == 0)
		tmp[n++] = '0';
	else
		for (; v > 0; v /= 10)
			tmp[n++] = '0' + (int)(v % 10);
	for (j = 0, s = n - 1; s >= 0; s--, j++)
		buf[j] = tmp[s];
	return j;
}

/* 从 /proc/stat 文本里读 btime（每台手机已是对的，时间命名空间调过）。读不到返回 0。 */
static long long pp_parse_btime(const char *in, int len)
{
	int i = 0;

	while (i < len) {
		int ls = i;

		while (i < len && in[i] != '\n')
			i++;
		if (i - ls > 6 && in[ls] == 'b' && in[ls + 1] == 't' && in[ls + 2] == 'i' &&
		    in[ls + 3] == 'm' && in[ls + 4] == 'e' && in[ls + 5] == ' ') {
			long long v = 0;
			int j = ls + 6;

			while (j < i && in[j] >= '0' && in[j] <= '9')
				v = v * 10 + (in[j++] - '0');
			return v;
		}
		i++;
	}
	return 0;
}

static void pp_stat_cpu_line(struct pp_out *o, const char *tag, int taglen, int agg, int ncpu, long long up_sec)
{
	unsigned long long ticks = (unsigned long long)up_sec * 100ULL;
	unsigned long long idle, sys, usr;
	char num[24];

	if (agg)
		ticks *= (unsigned)ncpu;
	idle = ticks * 97ULL / 100ULL;
	sys = ticks * 15ULL / 1000ULL;
	usr = ticks > idle + sys ? ticks - idle - sys : 0;
	pp_put(o, tag, taglen);
	pp_puts(o, agg ? "  " : " ");
	pp_put(o, num, pp_fmt_u64(usr, num));
	pp_puts(o, " 0 "); /* nice */
	pp_put(o, num, pp_fmt_u64(sys, num));
	pp_puts(o, " ");
	pp_put(o, num, pp_fmt_u64(idle, num));
	pp_puts(o, " 0 0 0 0 0 0\n"); /* iowait irq softirq steal guest guest_nice */
}

static void pp_stat_num_line(struct pp_out *o, const char *label, int llen, unsigned long long v)
{
	char num[24];

	pp_put(o, label, llen);
	pp_put(o, num, pp_fmt_u64(v, num));
	pp_puts(o, "\n");
}

static int pp_rewrite_stat(const char *in, int len, char *out, int max, int ncpu, long long up_sec)
{
	struct pp_out o = { out, 0, max, 0 };
	int i = 0;

	if (up_sec < 1)
		up_sec = 1;
	while (i < len) {
		int ls = i, le, tagend;

		while (i < len && in[i] != '\n')
			i++;
		le = (i < len) ? i + 1 : i;
		if (le - ls >= 4 && in[ls] == 'c' && in[ls + 1] == 'p' && in[ls + 2] == 'u') {
			tagend = ls + 3;
			while (tagend < le && in[tagend] >= '0' && in[tagend] <= '9')
				tagend++;
			pp_stat_cpu_line(&o, in + ls, tagend - ls, tagend == ls + 3, ncpu, up_sec);
		} else if (le - ls > 5 && memcmp(in + ls, "ctxt ", 5) == 0) {
			/* 上下文切换计数：宿主机累计、几台一样。按开机时长 × 一个合理速率 */
			pp_stat_num_line(&o, "ctxt ", 5, (unsigned long long)up_sec * 1800ULL);
		} else if (le - ls > 10 && memcmp(in + ls, "processes ", 10) == 0) {
			/* 开机以来 fork 的进程总数：同上 */
			pp_stat_num_line(&o, "processes ", 10, (unsigned long long)up_sec * 3ULL + 500ULL);
		} else {
			pp_put(&o, in + ls, le - ls);
		}
		i = le;
	}
	return o.bad ? -1 : o.n;
}

/* mode：PP_MOUNTS / PP_MOUNTINFO / PP_MOUNTSTATS / PP_NET / PP_INPUT / PP_UPTIME / PP_STAT */
#define PP_MOUNTS 0
#define PP_MOUNTINFO 1
#define PP_MOUNTSTATS 2
#define PP_NET 3
#define PP_INPUT 4
#define PP_UPTIME 5
#define PP_STAT 6

static int pp_rewrite(const char *in, int len, char *out, int max, int mode, const struct pp_cfg *c)
{
	switch (mode) {
	case PP_MOUNTINFO:
		return pp_rewrite_mountinfo(in, len, out, max, c);
	case PP_MOUNTSTATS:
		return pp_rewrite_mountstats(in, len, out, max, c);
	case PP_NET:
		return pp_rewrite_net(in, len);
	case PP_INPUT:
		return pp_rewrite_input(in, len);
	}
	return pp_rewrite_mounts(in, len, out, max, c);
}

#endif
