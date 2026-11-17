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

/* mode：PP_MOUNTS / PP_MOUNTINFO / PP_MOUNTSTATS */
#define PP_MOUNTS 0
#define PP_MOUNTINFO 1
#define PP_MOUNTSTATS 2

static int pp_rewrite(const char *in, int len, char *out, int max, int mode, const struct pp_cfg *c)
{
	switch (mode) {
	case PP_MOUNTINFO:
		return pp_rewrite_mountinfo(in, len, out, max, c);
	case PP_MOUNTSTATS:
		return pp_rewrite_mountstats(in, len, out, max, c);
	}
	return pp_rewrite_mounts(in, len, out, max, c);
}

#endif
