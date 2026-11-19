// SPDX-License-Identifier: GPL-2.0
/*
 * pp_mnt：云手机里读到的挂载表不带容器痕迹（踩坑 #68）。
 *
 * App 能读 /proc/self/mountinfo、/proc/self/mounts（/proc/mounts 是指向它的链接），里面有 Docker 的 overlay
 * （lowerdir=/var/lib/containerd/...）、宿主机目录（/opt/prism-phone/...）、Docker 塞的 resolv.conf 等——一眼就是容器。
 * 容器里用 bind 遮不住（/proc/mounts 是链接，各进程读各自的），只能在内核里改。
 *
 * 做法：kretprobe 挂在内核输出这几个文件每一行的函数（show_mountinfo / show_vfsmnt / show_vfsstat）上。进入时记下这一行在
 * seq_file 缓冲区里的起点；返回时如果读的进程属于登记过的云手机（按 PID 命名空间认：安卓给每个 App 单开挂载命名空间，
 * 但不开 PID 命名空间，容器里所有进程都在同一个），就按 rewrite.h 的规则改写 / 删掉刚输出的这一行。
 * 只改读到的文字，不动真实的挂载；宿主机和别的容器读到的不变。另外 statfs() 返回的 overlay 类型号改成 ext4（和挂载表一致）；
 * /proc/net/tcp、udp 里删掉云手机自己的本机端口和 Docker 内置 DNS 那几行（踩坑 #77）。
 *
 * 管家开机后写 /proc/pp_mnt（只有 root 能写）：
 *     add <容器 init 的 pid>|<这台手机 /data 在宿主机上的目录>|<那块盘在挂载表里的名字，如 /dev/sda1>
 *     del <容器 init 的 pid>
 * 读 /proc/pp_mnt 看登记了几台。容器停了以后，它的 PID 命名空间在下次 add 时自动清掉。
 */
#include <linux/module.h>
#include <linux/kprobes.h>
#include <linux/proc_fs.h>
#include <linux/seq_file.h>
#include <linux/uaccess.h>
#include <linux/pid.h>
#include <linux/pid_namespace.h>
#include <linux/sched.h>
#include <linux/sched/task.h>
#include <linux/spinlock.h>
#include <linux/percpu.h>
#include <linux/string.h>
#include <linux/statfs.h>
#include <linux/cpumask.h>
#include <linux/cred.h>
#include <linux/uidgid.h>
#include <linux/errno.h>

#include "rewrite.h"

#define PP_SLOTS 128
#define PP_LINE_MAX 2048
#define PP_DROP_MAX 16 /* 每次 add 最多顺手清掉几台已关的（引用在锁外放） */

struct pp_slot {
	struct pid_namespace *ns; /* 持有引用 */
	struct pp_cfg cfg;
};

static struct pp_slot slots[PP_SLOTS];
static DEFINE_RWLOCK(slots_lock);
static DEFINE_PER_CPU(char[PP_LINE_MAX], pp_buf);

/* PID 命名空间里的 init 退出后不再分配 pid（PIDNS_ADDING 被清掉）：手机已经关了 */
static bool pp_ns_dead(struct pid_namespace *ns)
{
#ifdef PIDNS_ADDING
	return !(READ_ONCE(ns->pid_allocated) & PIDNS_ADDING);
#else
	return false;
#endif
}

/* 调用时持有 slots_lock（读或写） */
static struct pp_slot *pp_find(struct pid_namespace *ns)
{
	int i;

	if (!ns || ns == &init_pid_ns)
		return NULL;
	for (i = 0; i < PP_SLOTS; i++)
		if (slots[i].ns == ns)
			return &slots[i];
	return NULL;
}

/* ---------- kretprobe ---------- */

struct pp_ri {
	struct seq_file *m;
	size_t start;
};

static int pp_entry(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	struct pp_ri *d = (struct pp_ri *)ri->data;
	struct pid_namespace *ns = task_active_pid_ns(current);
	bool hit;

	if (!ns || ns == &init_pid_ns)
		return 1; /* 宿主机进程：不装返回钩子 */
	read_lock(&slots_lock);
	hit = pp_find(ns) != NULL;
	read_unlock(&slots_lock);
	if (!hit)
		return 1;
	d->m = (struct seq_file *)regs_get_kernel_argument(regs, 0);
	d->start = d->m ? d->m->count : 0;
	return d->m ? 0 : 1;
}

static void pp_done(struct kretprobe_instance *ri, int mode)
{
	struct pp_ri *d = (struct pp_ri *)ri->data;
	struct seq_file *m = d->m;
	struct pp_slot *s;
	char *out;
	int n = -1;

	/* 没输出、或者缓冲区满了（seq_file 会换大缓冲区重来，到时候再改） */
	if (!m || !m->buf || m->count <= d->start || m->count >= m->size)
		return;
	out = this_cpu_ptr(pp_buf); /* 钩子里不会被抢占 */
	read_lock(&slots_lock);
	s = pp_find(task_active_pid_ns(current));
	if (s) {
		if (mode == PP_UPTIME)
			n = pp_rewrite_uptime(m->buf + d->start, (int)(m->count - d->start), out, PP_LINE_MAX, num_online_cpus());
		else
			n = pp_rewrite(m->buf + d->start, (int)(m->count - d->start), out, PP_LINE_MAX, mode, &s->cfg);
	}
	read_unlock(&slots_lock);
	if (n < 0)
		return;
	if (d->start + n >= m->size) {
		m->count = m->size; /* 放不下：标成溢出，seq_file 换大缓冲区重新输出这一行 */
		return;
	}
	memcpy(m->buf + d->start, out, n);
	m->count = d->start + n;
}

static int pp_ret_mountinfo(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	pp_done(ri, PP_MOUNTINFO);
	return 0;
}

static int pp_ret_mounts(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	pp_done(ri, PP_MOUNTS);
	return 0;
}

static int pp_ret_mountstats(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	pp_done(ri, PP_MOUNTSTATS);
	return 0;
}

/*
 * statfs()/fstatfs()：容器的根目录、/apex 等是 overlay，文件系统类型号是 OVERLAYFS_SUPER_MAGIC，
 * 和改写后挂载表说的 ext4 对不上。本内核里 vfs_statfs 被内联进了系统调用路径，挂它的返回只能抓到对下层
 * ext4 的那次调用（返回的已经是 ext4 类型号），抓不到带 overlay 类型号的那层（踩坑 #80）。真正带 overlay
 * 类型号的是 do_statfs_native / do_statfs64——把内核填好的 kstatfs 拷给用户前的最后一步，所以挂这两个函数的
 * 入口，进去前把 overlay 类型号改成 ext4（只改登记过的手机里的进程）。
 */
#define PP_OVERLAYFS_MAGIC 0x794c7630
#define PP_EXT4_MAGIC 0xEF53
/* overlay（根/系统/vendor 等只读系统分区）本来返回宿主机整块盘的大小（几台手机一样、一眼同一宿主机）。
 * 改成像手机只读系统分区：~6GB、基本占满（留一点点空闲，免得开机时有组件看到「0 空间」出岔）。踩坑 #92。 */
#define PP_SYS_SIZE_BYTES (6ULL << 30)
#define PP_SYS_FILES 300000u

static int pp_statfs_pre(struct kprobe *p, struct pt_regs *regs)
{
	struct kstatfs *st = (struct kstatfs *)regs_get_kernel_argument(regs, 0);
	struct pid_namespace *ns = task_active_pid_ns(current);
	bool hit;

	if (!ns || ns == &init_pid_ns || !st || st->f_type != PP_OVERLAYFS_MAGIC)
		return 0;
	read_lock(&slots_lock);
	hit = pp_find(ns) != NULL;
	read_unlock(&slots_lock);
	if (hit) {
		st->f_type = PP_EXT4_MAGIC;
		if (st->f_bsize >= 512) {
			st->f_blocks = PP_SYS_SIZE_BYTES / st->f_bsize;
			st->f_bfree = st->f_blocks / 64;  /* ~1.5% 空闲，像基本占满的只读系统分区 */
			st->f_bavail = st->f_bfree;
			st->f_files = PP_SYS_FILES;
			st->f_ffree = 0;
		}
	}
	return 0;
}

/* 不同内核这两个符号可能有一个不存在，best-effort 挂：哪个在挂哪个。 */
static struct kprobe statfs_probes[] = {
	{ .symbol_name = "do_statfs_native", .pre_handler = pp_statfs_pre },
	{ .symbol_name = "do_statfs64", .pre_handler = pp_statfs_pre },
};
static bool statfs_ok[ARRAY_SIZE(statfs_probes)];

static struct kretprobe rp_mountinfo = {
	.kp.symbol_name = "show_mountinfo",
	.entry_handler = pp_entry,
	.handler = pp_ret_mountinfo,
	.data_size = sizeof(struct pp_ri),
	.maxactive = 64,
};

static struct kretprobe rp_mounts = {
	.kp.symbol_name = "show_vfsmnt",
	.entry_handler = pp_entry,
	.handler = pp_ret_mounts,
	.data_size = sizeof(struct pp_ri),
	.maxactive = 64,
};

static struct kretprobe rp_mountstats = {
	.kp.symbol_name = "show_vfsstat",
	.entry_handler = pp_entry,
	.handler = pp_ret_mountstats,
	.data_size = sizeof(struct pp_ri),
	.maxactive = 64,
};

static struct kretprobe *probes[] = { &rp_mountinfo, &rp_mounts, &rp_mountstats };

/*
 * /proc/net/tcp、tcp6、udp、udp6（踩坑 #77）：云手机自己的本机端口（蓝牙芯片 6401~6404、基带 9600）和 Docker 的内置 DNS
 * （127.0.0.11）那几行删掉。尽量挂：哪个内核函数不存在就跳过那一个，不影响挂载表那几个。
 */
static int pp_ret_net(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	pp_done(ri, PP_NET);
	return 0;
}

#define PP_NET_PROBE(var, sym) \
	static struct kretprobe var = { .kp.symbol_name = sym, .entry_handler = pp_entry, .handler = pp_ret_net, \
					 .data_size = sizeof(struct pp_ri), .maxactive = 64 }
PP_NET_PROBE(rp_tcp4, "tcp4_seq_show");
PP_NET_PROBE(rp_tcp6, "tcp6_seq_show");
PP_NET_PROBE(rp_udp4, "udp4_seq_show");
PP_NET_PROBE(rp_udp6, "udp6_seq_show");
static struct kretprobe *net_probes[] = { &rp_tcp4, &rp_tcp6, &rp_udp4, &rp_udp6 };
static bool net_ok[ARRAY_SIZE(net_probes)];

/* /proc/bus/input/devices：删掉 QEMU / virtio 等虚拟机输入设备那几块（踩坑 #82）。 */
static int pp_ret_input(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	pp_done(ri, PP_INPUT);
	return 0;
}
static struct kretprobe rp_input = {
	.kp.symbol_name = "input_devices_seq_show",
	.entry_handler = pp_entry,
	.handler = pp_ret_input,
	.data_size = sizeof(struct pp_ri),
	.maxactive = 64,
};
static bool input_ok;

/* /proc/uptime（踩坑 #90）：空闲时间（第二个数）本来是宿主机累计的，几台一样、远大于本机开机时长，一看就不对。
 * 改成 ≈ 本机开机时长 × 在线 CPU 数 × 0.98（像刚开机不久、基本空闲的真机）。 */
static int pp_ret_uptime(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	pp_done(ri, PP_UPTIME);
	return 0;
}
static struct kretprobe rp_uptime = {
	.kp.symbol_name = "uptime_proc_show",
	.entry_handler = pp_entry,
	.handler = pp_ret_uptime,
	.data_size = sizeof(struct pp_ri),
	.maxactive = 16,
};
static bool uptime_ok;

/*
 * /proc/modules（踩坑 #91）：App（uid≥10000）能读到宿主机和我们自己的内核模块（pp_mnt / pp_uts…）一看就露馅。
 * 真机 SELinux 不让 App 读 proc_modules，open 直接 -EACCES。这里对「登记过的手机里、uid≥10000 的 App」把
 * modules_open 的成功返回改成 -EACCES；为避免泄漏，先 seq_release 掉 seq_open 已分配的 seq_file。
 * root / system（uid<10000）照常能读（真机上特权域也能读，保持一致）。modules_open 这个符号是唯一的（m_show 不是）。
 */
struct pp_open_ri {
	struct inode *inode;
	struct file *file;
};

static bool pp_app_in_phone(void)
{
	struct pid_namespace *ns = task_active_pid_ns(current);
	bool hit;

	if (!ns || ns == &init_pid_ns)
		return false;
	if (from_kuid(&init_user_ns, current_uid()) < 10000)
		return false;
	read_lock(&slots_lock);
	hit = pp_find(ns) != NULL;
	read_unlock(&slots_lock);
	return hit;
}

static int pp_modules_entry(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	struct pp_open_ri *d = (struct pp_open_ri *)ri->data;

	if (!pp_app_in_phone())
		return 1; /* 不是手机里的 App：不装返回钩子，照常 */
	d->inode = (struct inode *)regs_get_kernel_argument(regs, 0);
	d->file = (struct file *)regs_get_kernel_argument(regs, 1);
	return 0;
}

static int pp_modules_ret(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	struct pp_open_ri *d = (struct pp_open_ri *)ri->data;

	if (regs_return_value(regs) == 0 && d->file) {
		seq_release(d->inode, d->file); /* 释放 seq_open 分配的 seq_file，避免泄漏 */
		d->file->private_data = NULL;   /* 失败的 open 不会再调 release，置空多一层保险 */
		regs_set_return_value(regs, (unsigned long)(-EACCES));
	}
	return 0;
}

static struct kretprobe rp_modules = {
	.kp.symbol_name = "modules_open",
	.entry_handler = pp_modules_entry,
	.handler = pp_modules_ret,
	.data_size = sizeof(struct pp_open_ri),
	.maxactive = 16,
};
static bool modules_ok;

/* ---------- /proc/pp_mnt ---------- */

static struct pid_namespace *pp_ns_of(long nr)
{
	struct pid *pid = find_get_pid((pid_t)nr);
	struct task_struct *task;
	struct pid_namespace *ns = NULL;

	if (!pid)
		return NULL;
	task = get_pid_task(pid, PIDTYPE_PID);
	put_pid(pid);
	if (!task)
		return NULL;
	ns = task_active_pid_ns(task);
	if (ns)
		ns = get_pid_ns(ns);
	put_task_struct(task);
	return ns;
}

static ssize_t pp_mnt_write(struct file *file, const char __user *ubuf, size_t len, loff_t *off)
{
	char buf[PP_PREFIX_MAX + PP_SRC_MAX + 32];
	char *p, *cmd, *spid, *prefix, *src;
	struct pid_namespace *ns, *drop[PP_DROP_MAX + 2];
	struct pp_slot *s;
	long nr;
	int i, ndrop = 0, ret = 0;

	if (!capable(CAP_SYS_ADMIN))
		return -EPERM;
	if (len == 0 || len >= sizeof(buf))
		return -EINVAL;
	if (copy_from_user(buf, ubuf, len))
		return -EFAULT;
	buf[len] = '\0';
	p = strim(buf);
	cmd = strsep(&p, " ");
	if (!cmd || !p)
		return -EINVAL;
	spid = strsep(&p, "|");
	if (!spid || kstrtol(spid, 10, &nr) || nr <= 1)
		return -EINVAL;
	ns = pp_ns_of(nr);
	if (!ns)
		return -ESRCH;
	if (ns == &init_pid_ns) {
		put_pid_ns(ns);
		return -EPERM; /* 不改宿主机自己的 */
	}

	write_lock(&slots_lock);
	if (strcmp(cmd, "add") == 0) {
		prefix = strsep(&p, "|");
		src = p;
		if (!prefix || !src || strlen(prefix) >= PP_PREFIX_MAX || strlen(src) >= PP_SRC_MAX || prefix[0] != '/') {
			ret = -EINVAL;
			goto out;
		}
		/* 顺手清掉已经关了的手机 */
		for (i = 0; i < PP_SLOTS && ndrop < PP_DROP_MAX; i++)
			if (slots[i].ns && slots[i].ns != ns && pp_ns_dead(slots[i].ns)) {
				drop[ndrop++] = slots[i].ns;
				slots[i].ns = NULL;
			}
		s = pp_find(ns);
		if (s) {
			drop[ndrop++] = ns; /* 已经有一份引用了 */
		} else {
			for (i = 0; i < PP_SLOTS && slots[i].ns; i++)
				;
			if (i == PP_SLOTS) {
				ret = -ENOSPC;
				drop[ndrop++] = ns;
				goto out;
			}
			s = &slots[i];
			s->ns = ns;
		}
		strscpy(s->cfg.prefix, prefix, sizeof(s->cfg.prefix));
		strscpy(s->cfg.src, src, sizeof(s->cfg.src));
	} else if (strcmp(cmd, "del") == 0) {
		s = pp_find(ns);
		if (s) {
			drop[ndrop++] = s->ns;
			s->ns = NULL;
		}
		drop[ndrop++] = ns;
	} else {
		ret = -EINVAL;
		drop[ndrop++] = ns;
	}
out:
	write_unlock(&slots_lock);
	for (i = 0; i < ndrop; i++)
		put_pid_ns(drop[i]);
	return ret ? ret : (ssize_t)len;
}

static int pp_mnt_show(struct seq_file *m, void *v)
{
	int i, n = 0;

	read_lock(&slots_lock);
	for (i = 0; i < PP_SLOTS; i++)
		if (slots[i].ns && !pp_ns_dead(slots[i].ns))
			n++;
	read_unlock(&slots_lock);
	seq_printf(m, "phones %d\n", n);
	return 0;
}

static int pp_mnt_open(struct inode *inode, struct file *file)
{
	return single_open(file, pp_mnt_show, NULL);
}

static const struct proc_ops pp_mnt_ops = {
	.proc_open = pp_mnt_open,
	.proc_read = seq_read,
	.proc_lseek = seq_lseek,
	.proc_release = single_release,
	.proc_write = pp_mnt_write,
};

static struct proc_dir_entry *entry;

static int __init pp_mnt_init(void)
{
	int ret;

	/* 全挂上或全不挂 */
	ret = register_kretprobes(probes, ARRAY_SIZE(probes));
	if (ret) {
		pr_err("pp_mnt: 挂内核函数失败 %d（show_mountinfo / show_vfsmnt / show_vfsstat）\n", ret);
		return ret;
	}
	entry = proc_create("pp_mnt", 0600, NULL, &pp_mnt_ops);
	if (!entry) {
		unregister_kretprobes(probes, ARRAY_SIZE(probes));
		return -ENOMEM;
	}
	{
		int i;

		for (i = 0; i < ARRAY_SIZE(net_probes); i++) {
			net_ok[i] = register_kretprobe(net_probes[i]) == 0;
			if (!net_ok[i])
				pr_info("pp_mnt: 没挂上 %s（这个连接表照旧）\n", net_probes[i]->kp.symbol_name);
		}
		for (i = 0; i < ARRAY_SIZE(statfs_probes); i++) {
			statfs_ok[i] = register_kprobe(&statfs_probes[i]) == 0;
			if (!statfs_ok[i])
				pr_info("pp_mnt: 没挂上 %s（statfs 类型号照旧）\n", statfs_probes[i].symbol_name);
		}
		input_ok = register_kretprobe(&rp_input) == 0;
		if (!input_ok)
			pr_info("pp_mnt: 没挂上 input_devices_seq_show（/proc/bus/input/devices 照旧）\n");
		uptime_ok = register_kretprobe(&rp_uptime) == 0;
		if (!uptime_ok)
			pr_info("pp_mnt: 没挂上 uptime_proc_show（/proc/uptime 空闲时间照旧）\n");
		modules_ok = register_kretprobe(&rp_modules) == 0;
		if (!modules_ok)
			pr_info("pp_mnt: 没挂上 modules_open（/proc/modules 对 App 照旧）\n");
	}
	return 0;
}

static void __exit pp_mnt_exit(void)
{
	int i;

	proc_remove(entry);
	if (modules_ok)
		unregister_kretprobe(&rp_modules);
	if (uptime_ok)
		unregister_kretprobe(&rp_uptime);
	if (input_ok)
		unregister_kretprobe(&rp_input);
	for (i = 0; i < ARRAY_SIZE(statfs_probes); i++)
		if (statfs_ok[i])
			unregister_kprobe(&statfs_probes[i]);
	for (i = 0; i < ARRAY_SIZE(net_probes); i++)
		if (net_ok[i])
			unregister_kretprobe(net_probes[i]);
	unregister_kretprobes(probes, ARRAY_SIZE(probes));
	for (i = 0; i < PP_SLOTS; i++)
		if (slots[i].ns) {
			put_pid_ns(slots[i].ns);
			slots[i].ns = NULL;
		}
}

module_init(pp_mnt_init);
module_exit(pp_mnt_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("prism-phone: hide container traces in per-phone mount tables");
