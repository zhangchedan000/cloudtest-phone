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
	printf(fails ? "%d 项失败\n" : "全部通过\n", fails);
	return fails != 0;
}
