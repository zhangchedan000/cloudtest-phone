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
 *     判断办法：和 /data 同一块宿主机磁盘、但不在这台手机 /data 目录下的 bind，一律删。
 * 宿主机自己的进程不受影响（模块只改登记过的手机 PID 命名空间里的进程读到的内容）。
 */
#ifndef PP_MNT_REWRITE_H
#define PP_MNT_REWRITE_H

#define PP_PREFIX_MAX 256
#define PP_SRC_MAX 64
#define PP_MAX_TOK 40

struct pp_cfg {
	char prefix[PP_PREFIX_MAX]; /* 这台手机 /data 在宿主机上的目录，如 /opt/prism-phone/data/phones/<id>/data */
	char src[PP_SRC_MAX];       /* 那块宿主机磁盘在挂载表里的名字，如 /dev/sda1 */
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

/* 不管哪种格式都删的：mqueue、/dev/shm、不在 /dev/pts 的 devpts（小助手绑的伪终端）、Docker 塞的 /etc 文件 */
static int pp_always_drop(struct pp_tok fstype, struct pp_tok mnt)
{
	return pp_eq(fstype, "mqueue") || pp_eq(mnt, "/dev/shm") ||
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
	return -1;
}

/* /proc/mounts：来源 挂载点 类型 选项 0 0（没有 root 列，靠挂载点判断是不是 /data） */
static int pp_rewrite_mounts(const char *in, int len, char *out, int max, const struct pp_cfg *c)
{
	struct pp_tok t[8];
	struct pp_out o = { out, 0, max, 0 };
	int k = pp_split(in, len, t, 8);

	if (k < 4)
		return -1;
	if (pp_always_drop(t[2], t[1]))
		return 0;
	if (pp_eq(t[2], "overlay")) {
		pp_puts(&o, PP_SYS_DEV " ");
		pp_putt(&o, t[1]);
		pp_puts(&o, " ext4 " PP_SYS_OPTS " 0 0\n");
		return o.bad ? -1 : o.n;
	}
	if (c->src[0] && pp_eq(t[0], c->src)) {
		if (!(pp_eq(t[1], "/data") || pp_starts(t[1], "/data/") || pp_starts(t[1], "/data_mirror/")))
			return 0;
		pp_puts(&o, PP_DATA_DEV " ");
		pp_putt(&o, t[1]);
		pp_puts(&o, " ");
		pp_putt(&o, t[2]);
		pp_puts(&o, " " PP_DATA_OPTS " 0 0\n");
		return o.bad ? -1 : o.n;
	}
	return -1;
}

static int pp_rewrite(const char *in, int len, char *out, int max, int mi, const struct pp_cfg *c)
{
	return mi ? pp_rewrite_mountinfo(in, len, out, max, c) : pp_rewrite_mounts(in, len, out, max, c);
}

#endif
