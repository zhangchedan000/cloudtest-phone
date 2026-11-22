// 用户态测试 pp_mnt 的改写规则（和内核模块同一份 rewrite.h）：bash host/kmod/pp_mnt/test.sh
// 样本是 2026-11-16 从甲骨文上一台云手机的 /proc/self/mountinfo、/proc/mounts 里抄的。
#include <stdio.h>
#include <string.h>
#include "rewrite.h"

static int fails;

static void check(int mi, const char *in, const char *want)
{
	static const struct pp_cfg c = { "/opt/prism-phone/data/phones/378ad10f54b8/data", "/dev/sda1" };
	char out[2048];
	int n = pp_rewrite(in, (int)strlen(in), out, sizeof(out), mi, &c);
	const char *got = n < 0 ? in : (n == 0 ? "" : (out[n] = 0, out));

	if (strcmp(got, want) != 0) {
		fails++;
		printf("FAIL\n  in:   %s  got:  %s  want: %s", in, got[0] ? got : "(删掉)\n", want[0] ? want : "(删掉)\n");
	}
}

#define OVL "overlay overlay rw,lowerdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/522/fs:/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/492/fs,upperdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/523/fs,workdir=/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/523/work"
#define SDA "ext4 /dev/sda1 rw,discard,errors=remount-ro,commit=30"
#define P "/opt/prism-phone/data/phones/378ad10f54b8"

int main(void)
{
	/* mountinfo */
	check(1, "482 156 0:53 / / rw,relatime shared:404 - " OVL "\n",
	      "482 156 253:0 / / ro,relatime shared:404 - ext4 /dev/block/dm-0 ro,seclabel,relatime\n");
	check(1, "537 197 0:53 /system/apex/com.android.art /apex/com.android.art rw,relatime shared:404 - " OVL "\n",
	      "537 197 253:0 /system/apex/com.android.art /apex/com.android.art ro,relatime shared:404 - ext4 /dev/block/dm-0 ro,seclabel,relatime\n");
	check(1, "495 482 8:1 " P "/data /data rw,relatime shared:524 - " SDA "\n",
	      "495 482 253:34 / /data rw,relatime shared:524 - ext4 /dev/block/dm-34 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic\n");
	check(1, "642 641 8:1 " P "/data/user /data_mirror/data_ce/null rw,relatime shared:524 - " SDA "\n",
	      "642 641 253:34 /user /data_mirror/data_ce/null rw,relatime shared:524 - ext4 /dev/block/dm-34 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic\n");
	check(1, "498 482 8:1 " P "/buildprop/0.prop /system/build.prop ro,relatime shared:555 - " SDA "\n", "");
	check(1, "499 482 8:1 /var/lib/docker/containers/95c0/resolv.conf /etc/resolv.conf rw,relatime shared:556 - " SDA "\n", "");
	check(1, "620 194 8:1 " P "/data/.empty /proc/vmallocinfo rw,relatime shared:524 - " SDA "\n", "");
	check(1, "490 485 0:69 / /dev/mqueue rw,nosuid,nodev,noexec,relatime shared:489 - mqueue mqueue rw\n", "");
	check(1, "493 485 0:75 / /dev/shm rw,nosuid,nodev,noexec,relatime shared:490 - tmpfs shm rw,size=65536k,inode64\n", "");
	check(1, "153 485 0:73 /0 /dev/goldfish_pipe_dprctd rw,nosuid,noexec,relatime shared:491 - devpts devpts rw,gid=5,mode=620,ptmxmode=666\n", "");
	check(1, "155 486 0:55 / /dev/pts rw,relatime shared:488 - devpts devpts rw,mode=600,ptmxmode=000\n",
	      "155 486 0:55 / /dev/pts rw,relatime shared:488 - devpts devpts rw,mode=600,ptmxmode=000\n");
	check(1, "194 484 0:56 / /proc rw,relatime shared:464 - proc proc rw,gid=3009,hidepid=invisible\n",
	      "194 484 0:56 / /proc rw,relatime shared:464 - proc proc rw,gid=3009,hidepid=invisible\n");
	/* 别的手机的目录（前缀相同开头但不是它）不能当成自己的 /data */
	check(1, "700 482 8:1 " P "/data2 /x rw,relatime - " SDA "\n", "");
	/* 没有可选字段 */
	check(1, "197 482 0:68 / /apex rw,nosuid,nodev,noexec,relatime - tmpfs tmpfs rw,mode=755,inode64\n",
	      "197 482 0:68 / /apex rw,nosuid,nodev,noexec,relatime - tmpfs tmpfs rw,mode=755,inode64\n");

	check(1, "1903 623 0:131 / /storage/emulated rw,nosuid,nodev,noexec,noatime master:494 - fuse /dev/fuse rw,lazytime,user_id=0,group_id=0,allow_other\n",
	      "1903 623 0:131 / /storage/emulated rw,nosuid,nodev,noexec,noatime master:494 - fuse /dev/fuse rw,lazytime,user_id=0,group_id=0,allow_other\n");
	check(1, "5442 194 0:119 /proc/meminfo /proc/meminfo ro,nosuid,nodev,relatime shared:541 - fuse.lxcfs lxcfs rw,user_id=0,group_id=0,allow_other\n", "");
	check(1, "4450 194 8:1 " P "/fake/cpuinfo /proc/cpuinfo ro,relatime shared:549 - " SDA "\n", "");
	check(1, "2100 195 8:1 " P "/data/media /mnt/pass_through/0/emulated rw,relatime shared:524 - " SDA "\n",
	      "2100 195 253:34 /media /mnt/pass_through/0/emulated rw,relatime shared:524 - ext4 /dev/block/dm-34 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic\n");

	/* /proc/mounts */
	check(0, "/dev/fuse /storage/emulated fuse rw,lazytime,nosuid 0 0\n", "/dev/fuse /storage/emulated fuse rw,lazytime,nosuid 0 0\n");
	check(0, "lxcfs /proc/meminfo fuse.lxcfs ro,nosuid 0 0\n", "");
	check(0, "/dev/sda1 /proc/cpuinfo ext4 ro,relatime 0 0\n", "");
	check(0, "/dev/sda1 /mnt/pass_through/0/emulated ext4 rw,relatime 0 0\n",
	      "/dev/block/dm-34 /mnt/pass_through/0/emulated ext4 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic 0 0\n");
	check(0, "/dev/sda1 /storage/emulated/0/Android/data ext4 rw,relatime 0 0\n",
	      "/dev/block/dm-34 /storage/emulated/0/Android/data ext4 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic 0 0\n");
	check(0, "/dev/sda1 /sys/devices/system/cpu/cpu0/regs/identification/midr_el1 ext4 ro 0 0\n", "");
	check(0, OVL " 0 0\n", OVL " 0 0\n");  /* 挂载点不是路径的怪行（第 3 列不是 overlay）：原样 */
	check(0, "overlay / " "overlay rw,relatime,lowerdir=/var/lib/containerd/a:/b 0 0\n", "/dev/block/dm-0 / ext4 ro,seclabel,relatime 0 0\n");
	check(0, "/dev/sda1 /data ext4 rw,relatime,discard 0 0\n", "/dev/block/dm-34 /data ext4 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic 0 0\n");
	check(0, "/dev/sda1 /data_mirror/data_de/null ext4 rw,relatime 0 0\n", "/dev/block/dm-34 /data_mirror/data_de/null ext4 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic 0 0\n");
	check(0, "/dev/sda1 /system/build.prop ext4 ro,relatime 0 0\n", "");
	check(0, "/dev/sda1 /etc/hosts ext4 rw,relatime 0 0\n", "");
	check(0, "/dev/sda1 /proc/vmallocinfo ext4 rw,relatime 0 0\n", "");
	check(0, "shm /dev/shm tmpfs rw,nosuid 0 0\n", "");
	check(0, "proc /proc proc rw,relatime 0 0\n", "proc /proc proc rw,relatime 0 0\n");

	/* mountstats */
	check(2, "device overlay mounted on / with fstype overlay\n", "device /dev/block/dm-0 mounted on / with fstype ext4\n");
	check(2, "device /dev/sda1 mounted on /data with fstype ext4\n", "device /dev/block/dm-34 mounted on /data with fstype ext4\n");
	check(2, "device /dev/sda1 mounted on /proc/cpuinfo with fstype ext4\n", "");
	check(2, "device lxcfs mounted on /proc/meminfo with fstype fuse.lxcfs\n", "");
	check(2, "device mqueue mounted on /dev/mqueue with fstype mqueue\n", "");
	check(2, "device proc mounted on /proc with fstype proc\n", "device proc mounted on /proc with fstype proc\n");
	check(2, "device /dev/fuse mounted on /storage/emulated with fstype fuse\n", "device /dev/fuse mounted on /storage/emulated with fstype fuse\n");

	/* /proc/net/tcp、udp：云手机自己的端口和 Docker 的 DNS 删掉，别的原样 */
	check(3, "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n",
	      "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n");
	check(3, "   0: 00000000:1902 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 123 1\n", "");
	check(3, "   1: 0100007F:2580 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 124 1\n", "");
	check(3, "   2: 0B00007F:9A7F 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 125 1\n", "");
	check(3, "11233: 0B00007F:C879 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 76663959 2\n", "");
	check(3, "   3: C508A8C0:DF19 040012C6:01BB 01 00000000:00000000 00:00000000 00000000 10105 0 77310736 2\n",
	      "   3: C508A8C0:DF19 040012C6:01BB 01 00000000:00000000 00:00000000 00000000 10105 0 77310736 2\n");
	check(3, "   4: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A 0 0\n",
	      "   4: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A 0 0\n");

	/* /proc/bus/input/devices：QEMU / virtio 的输入设备整块删，真机的（fts_ts、电源键）原样 */
	check(4, "I: Bus=0003 Vendor=0627 Product=0001\nN: Name=\"QEMU QEMU USB Tablet\"\nH: Handlers=mouse0 event1\n\n", "");
	check(4, "I: Bus=0003 Vendor=0627\nN: Name=\"QEMU QEMU USB Keyboard\"\nH: Handlers=kbd event2\n\n", "");
	/* 魔云腾宿主机的 LG 虚拟输入设备整块删 */
	check(4, "I: Bus=0003 Vendor=1004 Product=6000\nN: Name=\"LG TouchScreen:7\"\nS: Sysfs=/devices/virtual/input/input40\n\n", "");
	check(4, "I: Bus=0003 Vendor=1004\nN: Name=\"LG KeyBoard:12\"\nH: Handlers=kbd event50\n\n", "");
	check(4, "I: Bus=0018 Vendor=0000\nN: Name=\"fts_ts\"\nH: Handlers=event3\n\n",
	      "I: Bus=0018 Vendor=0000\nN: Name=\"fts_ts\"\nH: Handlers=event3\n\n");
	check(4, "I: Bus=0019 Vendor=0000\nN: Name=\"Power Button\"\nH: Handlers=kbd event0\n\n",
	      "I: Bus=0019 Vendor=0000\nN: Name=\"Power Button\"\nH: Handlers=kbd event0\n\n");
	check(4, "I: Bus=0006 Vendor=0000\nN: Name=\"virtio_input\"\nH: Handlers=event4\n\n", "");

	/* 磁盘镜像的手机：/data 是整块 /dev/loop5，build.prop 在另一块盘 /dev/sda1 */
	{
		static const struct pp_cfg c = { "/", "/dev/loop5" };
		const struct { const char *in, *want; int mi; } cases[] = {
			{ "495 482 7:5 / /data rw,noatime shared:524 - ext4 /dev/loop5 rw\n",
			  "495 482 253:34 / /data rw,noatime shared:524 - ext4 /dev/block/dm-34 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic\n", 1 },
			{ "642 641 7:5 /user /data_mirror/data_ce/null rw,noatime - ext4 /dev/loop5 rw\n",
			  "642 641 253:34 /user /data_mirror/data_ce/null rw,noatime - ext4 /dev/block/dm-34 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic\n", 1 },
			{ "620 194 7:5 /.empty /proc/vmallocinfo rw,noatime - ext4 /dev/loop5 rw\n", "", 1 },
			{ "498 482 8:1 " P "/buildprop/0.prop /system/build.prop ro,relatime - " SDA "\n", "", 1 },
			{ "501 482 8:1 /some/other/host/file /x ro,relatime - " SDA "\n", "", 1 },
			{ "/dev/sda1 /system/build.prop ext4 ro,relatime 0 0\n", "", 0 },
			{ "/dev/loop5 /data ext4 rw,noatime 0 0\n", "/dev/block/dm-34 /data ext4 rw,seclabel,nosuid,nodev,noatime,resgid=1065,errors=panic 0 0\n", 0 },
		};
		for (unsigned i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
			char out[2048];
			int n = pp_rewrite(cases[i].in, (int)strlen(cases[i].in), out, sizeof(out), cases[i].mi, &c);
			const char *got = n < 0 ? cases[i].in : (n == 0 ? "" : (out[n] = 0, out));
			if (strcmp(got, cases[i].want) != 0) {
				fails++;
				printf("FAIL 磁盘镜像 #%u\n  got:  %s\n  want: %s\n", i, got, cases[i].want);
			}
		}
	}

	/* 输出放不下：原样（不截断） */
	{
		static const struct pp_cfg c = { P "/data", "/dev/sda1" };
		const char *in = "482 156 0:53 / / rw,relatime shared:404 - " OVL "\n";
		char small[20];
		if (pp_rewrite(in, (int)strlen(in), small, sizeof(small), 1, &c) != -1) {
			fails++;
			printf("FAIL 放不下时应该原样\n");
		}
	}
	/* /proc/uptime：空闲时间 = 开机时长 × 在线CPU数 × 0.98（厘秒整数运算） */
	{
		const struct { const char *in; int ncpu; const char *want; } up[] = {
			{ "1500.00 987654.32\n", 4, "1500.00 5880.00\n" },
			{ "1234.56 999999.99\n", 4, "1234.56 4839.47\n" },
			{ "100.00 50000.00\n", 8, "100.00 784.00\n" },
			{ "0.00 12345.00\n", 4, "0.00 0.00\n" },
			{ "1500.00\n", 4, "1500.00\n" },   /* 只有一个数：原样 */
			{ "1500.00 10.00\n", 0, "1500.00 10.00\n" }, /* ncpu 非法：原样 */
		};
		for (unsigned i = 0; i < sizeof(up) / sizeof(up[0]); i++) {
			char out[64];
			int n = pp_rewrite_uptime(up[i].in, (int)strlen(up[i].in), out, sizeof(out), up[i].ncpu, 0);
			const char *got = n < 0 ? up[i].in : (n == 0 ? "" : (out[n] = 0, out));
			if (strcmp(got, up[i].want) != 0) {
				fails++;
				printf("FAIL uptime #%u\n  got:  %s  want: %s", i, got, up[i].want);
			}
		}
	}

	/* /proc/stat：cpu / cpuN 行按本机开机时长重算，btime 等其余行原样 */
	{
		const char *in =
			"cpu  590396227 937301 22831775 3340156651 897753 0 123456 0 0 0\n"
			"cpu0 147599056 234325 5707943 835039162 224438 0 30864 0 0 0\n"
			"cpu1 147599057 234326 5707944 835039163 224439 0 30865 0 0 0\n"
			"intr 999 1 2 3\n"
			"ctxt 123456789\n"
			"btime 1795064107\n"
			"processes 54321\n"
			"procs_running 2\n"
			"procs_blocked 0\n"
			"softirq 888 1 2 3\n";
		const char *want =
			"cpu  4200 0 4200 271600 0 0 0 0 0 0\n"   /* 700*100*4=280000; idle97% sys1.5% usr补 */
			"cpu0 1050 0 1050 67900 0 0 0 0 0 0\n"    /* 700*100=70000 */
			"cpu1 1050 0 1050 67900 0 0 0 0 0 0\n"
			"intr 999 1 2 3\n"
			"ctxt 1260000\n"           /* 700*1800 */
			"btime 1795064107\n"
			"processes 2600\n"         /* 700*3+500 */
			"procs_running 2\n"
			"procs_blocked 0\n"
			"softirq 888 1 2 3\n";
		char out[2048];
		int n = pp_rewrite_stat(in, (int)strlen(in), out, sizeof(out), 4, 700, 0, 0);
		out[n < 0 ? 0 : n] = 0;
		if (n < 0 || strcmp(out, want) != 0) {
			fails++;
			printf("FAIL stat\n--- got ---\n%s--- want ---\n%s", n < 0 ? "(放不下)\n" : out, want);
		}
		/* btime 给了就换（Q1 内核的 btime 是宿主机的，按本机开机时长算的值替换） */
		n = pp_rewrite_stat(in, (int)strlen(in), out, sizeof(out), 4, 700, 1795330000LL, 0);
		out[n < 0 ? 0 : n] = 0;
		if (n < 0 || !strstr(out, "\nbtime 1795330000\n") || strstr(out, "1795064107")) {
			fails++;
			printf("FAIL stat btime 替换\n%s", out);
		}
		/* 每台手机一个 seed：同一秒开机的两台也不一样；聚合行 = 各核之和；各核不全一样；idle 在 93%~99% */
		{
			char o1[2048], o2[2048];
			unsigned long long a[5], c[4][5], sum[5] = { 0 };
			int n1 = pp_rewrite_stat(in, (int)strlen(in), o1, sizeof(o1), 4, 700, 0, 0x9e3779b1u);
			int n2 = pp_rewrite_stat(in, (int)strlen(in), o2, sizeof(o2), 4, 700, 0, 0x7f4a7c15u);
			o1[n1 < 0 ? 0 : n1] = 0;
			o2[n2 < 0 ? 0 : n2] = 0;
			if (n1 < 0 || n2 < 0 || strcmp(o1, o2) == 0) {
				fails++;
				printf("FAIL stat seed：两台一样\n%s", o1);
			}
			if (sscanf(o1, "cpu  %llu %*u %llu %llu", &a[0], &a[1], &a[2]) != 3) {
				fails++;
				printf("FAIL stat seed 解析\n%s", o1);
			} else {
				const char *q = o1;
				for (int k = 0; k < 2; k++) {
					q = strstr(q, k == 0 ? "\ncpu0 " : "\ncpu1 ");
					if (!q || sscanf(q + 1, "%*s %llu %*u %llu %llu", &c[k][0], &c[k][1], &c[k][2]) != 3) {
						fails++;
						printf("FAIL stat seed 核 %d\n", k);
						break;
					}
				}
				/* 输入只有 cpu0、cpu1 两行，ncpu=4：聚合按 4 核算，所以只核对比例和核间差异 */
				if (c[0][2] == c[1][2])
					printf("（cpu0 / cpu1 idle 相同，seed 下应略不同）\n"), fails++;
				if (a[2] * 100 < (a[0] + a[1] + a[2]) * 93 || a[2] * 100 > (a[0] + a[1] + a[2]) * 99) {
					fails++;
					printf("FAIL stat seed idle 比例\n%s", o1);
				}
				(void)sum;
			}
			/* uptime 的空闲 = 各核 idle 之和 */
			{
				char uo[64];
				int un = pp_rewrite_uptime("700.00 99999.00\n", 16, uo, sizeof(uo), 4, 0x9e3779b1u);
				unsigned long long tot = 0;
				struct pp_cpuv v;
				char want[64];
				for (int k = 0; k < 4; k++) {
					pp_core(70000, 0x9e3779b1u, k, &v);
					tot += v.idle;
				}
				snprintf(want, sizeof(want), "700.00 %llu.%02llu\n", tot / 100, tot % 100);
				uo[un < 0 ? 0 : un] = 0;
				if (un < 0 || strcmp(uo, want) != 0) {
					fails++;
					printf("FAIL uptime seed：%s want %s", uo, want);
				}
			}
		}
		if (pp_parse_btime(in, (int)strlen(in)) != 1795064107LL) {
			fails++;
			printf("FAIL stat btime 解析：%lld\n", pp_parse_btime(in, (int)strlen(in)));
		}
	}

	printf(fails ? "%d 项失败\n" : "全部通过\n", fails);
	return fails != 0;
}
