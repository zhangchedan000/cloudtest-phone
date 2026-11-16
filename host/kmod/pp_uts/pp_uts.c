// SPDX-License-Identifier: GPL-2.0
/*
 * pp_uts：给每台云手机（容器）设自己的内核版本号（踩坑 #64）。
 *
 * App 调 uname() 拿到的 release / version 来自进程所在的 UTS 命名空间。Docker 给每个容器一个新的 UTS 命名空间，
 * 但创建时是从宿主机复制的，只有主机名能改（sethostname），版本号没有系统调用能改——所以手机里 uname 一直是宿主机内核
 * （6.17.0-oracle）。这个模块提供 /proc/pp_uts（只有 root 能写），管家开机后写一行：
 *
 *     <容器 init 的 pid>|<release>|<version>
 *
 * 模块找到这个进程的 UTS 命名空间，把 release、version 改掉。只改容器自己的那份，拒绝改宿主机的（init_uts_ns）。
 * /proc/sys/kernel/osrelease、/proc/sys/kernel/version 也读同一份，跟着变。
 */
#include <linux/module.h>
#include <linux/proc_fs.h>
#include <linux/uaccess.h>
#include <linux/pid.h>
#include <linux/sched.h>
#include <linux/sched/task.h>
#include <linux/nsproxy.h>
#include <linux/utsname.h>
#include <linux/string.h>

#define PP_MAX 512

static ssize_t pp_uts_write(struct file *file, const char __user *ubuf, size_t len, loff_t *off)
{
	char buf[PP_MAX];
	char *p, *spid, *rel, *ver;
	long pid_nr;
	struct pid *pid;
	struct task_struct *task;
	struct uts_namespace *ns;
	int ret = -ESRCH;

	if (!capable(CAP_SYS_ADMIN))
		return -EPERM;
	if (len == 0 || len >= PP_MAX)
		return -EINVAL;
	if (copy_from_user(buf, ubuf, len))
		return -EFAULT;
	buf[len] = '\0';
	p = strim(buf);
	spid = strsep(&p, "|");
	rel = strsep(&p, "|");
	ver = p;
	if (!spid || !rel || !ver || !*rel || !*ver)
		return -EINVAL;
	if (kstrtol(spid, 10, &pid_nr) || pid_nr <= 1)
		return -EINVAL;
	if (strlen(rel) > __NEW_UTS_LEN || strlen(ver) > __NEW_UTS_LEN)
		return -E2BIG;

	pid = find_get_pid((pid_t)pid_nr);
	if (!pid)
		return -ESRCH;
	task = get_pid_task(pid, PIDTYPE_PID);
	put_pid(pid);
	if (!task)
		return -ESRCH;
	/* task_lock 期间 nsproxy 不会换、命名空间不会被释放 */
	task_lock(task);
	if (task->nsproxy) {
		ns = task->nsproxy->uts_ns;
		if (ns == &init_uts_ns) {
			ret = -EPERM; /* 不改宿主机自己的 */
		} else {
			strscpy(ns->name.release, rel, sizeof(ns->name.release));
			strscpy(ns->name.version, ver, sizeof(ns->name.version));
			ret = 0;
		}
	}
	task_unlock(task);
	put_task_struct(task);
	return ret ? ret : (ssize_t)len;
}

static const struct proc_ops pp_uts_ops = {
	.proc_write = pp_uts_write,
};

static struct proc_dir_entry *entry;

static int __init pp_uts_init(void)
{
	entry = proc_create("pp_uts", 0200, NULL, &pp_uts_ops);
	return entry ? 0 : -ENOMEM;
}

static void __exit pp_uts_exit(void)
{
	proc_remove(entry);
}

module_init(pp_uts_init);
module_exit(pp_uts_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("prism-phone: per-container kernel release/version");
