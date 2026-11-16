package phones

import "testing"

func TestDataMount(t *testing.T) {
	dir := "482 156 0:53 / / rw,relatime shared:404 - overlay overlay rw,lowerdir=/a\n" +
		"495 482 8:1 /opt/prism-phone/data/phones/x/data /data rw,relatime shared:524 - ext4 /dev/sda1 rw,discard\n" +
		"642 641 8:1 /opt/prism-phone/data/phones/x/data/user /data_mirror/data_ce/null rw,relatime shared:524 - ext4 /dev/sda1 rw\n"
	if root, src, ok := dataMount(dir); !ok || root != "/opt/prism-phone/data/phones/x/data" || src != "/dev/sda1" {
		t.Fatalf("目录：%q %q %v", root, src, ok)
	}
	img := "495 482 7:5 / /data rw,noatime - ext4 /dev/loop5 rw\n"
	if root, src, ok := dataMount(img); !ok || root != "/" || src != "/dev/loop5" {
		t.Fatalf("磁盘镜像：%q %q %v", root, src, ok)
	}
	if _, _, ok := dataMount("482 156 0:53 / / rw - overlay overlay rw\n"); ok {
		t.Fatal("没有 /data 不该 ok")
	}
}
