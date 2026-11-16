// SPDX-License-Identifier: GPL-2.0
/*
 * pp_mnt：云手机里读到的挂载表不带容器痕迹（踩坑 #68）。
 *
 * App 能读 /proc/self/mountinfo、/proc/self/mounts（/proc/mounts 是指向它的链接），里面有 Docker 的 overlay
 * （lowerdir=/var/lib/containerd/...）、宿主机目录（/opt/prism-phone/...）、Docker 塞的 resolv.conf 等——一眼就是容器。
 * 容器里用 bind 遮不住（/proc/mounts 是链接，各进程读各自的），只能在内核里改。
 *
 * 做法：kretprobe 挂在内核输出这两个文件每一行的函数（show_mountinfo / show_vfsmnt）上。进入时记下这一行在
 * seq_file 缓冲区里的起点；返回时如果读的进程属于登记过的云手机（按 PID 命名空间认：安卓给每个 App 单开挂载命名空间，
 * 但不开 PID 命名空间，容器里所有进程都在同一个），就按 rewrite.h 的规则改写 / 删掉刚输出的这一行。
 * 只改读到的文字，不动真实的挂载；宿主机和别的容器读到的不变。
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

static void pp_done(struct kretprobe_instance *ri, int mi)
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
	if (s)
		n = pp_rewrite(m->buf + d->start, (int)(m->count - d->start), out, PP_LINE_MAX, mi, &s->cfg);
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
	pp_done(ri, 1);
	return 0;
}

static int pp_ret_mounts(struct kretprobe_instance *ri, struct pt_regs *regs)
{
	pp_done(ri, 0);
	return 0;
}

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

	ret = register_kretprobe(&rp_mountinfo);
	if (ret) {
		pr_err("pp_mnt: 挂 show_mountinfo 失败 %d\n", ret);
		return ret;
	}
	ret = register_kretprobe(&rp_mounts);
	if (ret) {
		pr_err("pp_mnt: 挂 show_vfsmnt 失败 %d\n", ret);
		unregister_kretprobe(&rp_mountinfo);
		return ret;
	}
	entry = proc_create("pp_mnt", 0600, NULL, &pp_mnt_ops);
	if (!entry) {
		unregister_kretprobe(&rp_mounts);
		unregister_kretprobe(&rp_mountinfo);
		return -ENOMEM;
	}
	return 0;
}

static void __exit pp_mnt_exit(void)
{
	int i;

	proc_remove(entry);
	unregister_kretprobe(&rp_mounts);
	unregister_kretprobe(&rp_mountinfo);
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
