// Package wifi 云手机的虚拟 WiFi（踩坑 #57）。
//
// 每台手机：
//   - 一对 mac80211_hwsim 虚拟无线网卡（内核自带的 WiFi 模拟器，安卓官方模拟器也用它）：
//     一块给安卓当 wlan0（MAC 用机型档案的 wifiMac），一块当「家里路由器」的热点；
//   - 一个独立的「路由器」网络命名空间 ppr-<手机ID>：热点、DHCP、NAT 都在里面，
//     上行网卡就是 Docker 原来分给手机的 eth0（10.88.x），所以网络模块按 IP 分流到代理的规则完全不变；
//   - 手机自己的网络命名空间里只剩 lo 和 wlan0，和真手机一样没有 eth0，安卓看到的网络类型是 WIFI。
//
// 隔离：hwsim 的虚拟电波只在同一个「netgroup」里互通，netgroup 按创建网卡时所在的网络命名空间分。
// 所以两块网卡都在这台手机的路由器命名空间里创建（netlink 请求从那个命名空间发出），手机之间互相扫不到。
// 路由器命名空间删除时，内核自动删掉这个 netgroup 里的网卡。
package wifi

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"syscall"
)

const (
	genlIDCtrl          = 0x10
	ctrlCmdGetFamily    = 3
	ctrlAttrFamilyID    = 1
	ctrlAttrFamilyName  = 2
	hwsimCmdNewRadio    = 4
	hwsimAttrRadioName  = 17
	hwsimAttrNoVIF      = 18
	hwsimAttrPermAddr   = 22
	hwsimAttrSupportP2P = 14
	cloneNewNet         = 0x40000000
)

// NewRadioInNetns 在指定网络命名空间（/run/netns/<name>）里建一块 hwsim 网卡，返回内核分配的编号。
// name 是 wiphy 名字（phy 名，用来后续找到它），mac 是永久 MAC（安卓读到的「出厂 MAC」）。
// 不建默认网卡（NO_VIF），由调用方用 iw 按需要的名字和类型建。
func NewRadioInNetns(netnsPath, name string, mac [6]byte) (int, error) {
	type result struct {
		id  int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		// 网络命名空间是按线程的：锁住这个线程、切进去、建网卡；不切回来，goroutine 结束时 Go 会直接丢掉这个线程。
		runtime.LockOSThread()
		fd, err := os.Open(netnsPath)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer fd.Close()
		if _, _, e := syscall.RawSyscall(sysSetns, fd.Fd(), cloneNewNet, 0); e != 0 {
			ch <- result{err: fmt.Errorf("进入网络命名空间：%v", e)}
			return
		}
		id, err := newRadio(name, mac)
		ch <- result{id, err}
	}()
	r := <-ch
	return r.id, r.err
}

func newRadio(name string, mac [6]byte) (int, error) {
	s, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_GENERIC)
	if err != nil {
		return 0, fmt.Errorf("netlink：%w", err)
	}
	defer syscall.Close(s)
	if err := syscall.Bind(s, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return 0, err
	}
	fam, err := familyID(s, "MAC80211_HWSIM")
	if err != nil {
		return 0, fmt.Errorf("找不到 mac80211_hwsim（modprobe mac80211_hwsim radios=0）：%w", err)
	}
	attrs := append(nlAttr(hwsimAttrRadioName, append([]byte(name), 0)), nlAttr(hwsimAttrPermAddr, mac[:])...)
	attrs = append(attrs, nlAttr(hwsimAttrNoVIF, nil)...)
	attrs = append(attrs, nlAttr(hwsimAttrSupportP2P, nil)...)
	code, _, err := request(s, fam, hwsimCmdNewRadio, attrs)
	if err != nil {
		return 0, err
	}
	// hwsim 用 ack 的 error 字段返回新网卡编号（>=0）；负数是错误码
	if code < 0 {
		return 0, fmt.Errorf("建 hwsim 网卡失败：%v", syscall.Errno(-code))
	}
	return code, nil
}

func familyID(s int, name string) (uint16, error) {
	_, payload, err := request(s, genlIDCtrl, ctrlCmdGetFamily, nlAttr(ctrlAttrFamilyName, append([]byte(name), 0)))
	if err != nil {
		return 0, err
	}
	for off := 4; off+4 <= len(payload); {
		l := int(binary.LittleEndian.Uint16(payload[off:]))
		t := binary.LittleEndian.Uint16(payload[off+2:]) & 0x3fff
		if l < 4 || off+l > len(payload) {
			break
		}
		if t == ctrlAttrFamilyID && l >= 6 {
			return binary.LittleEndian.Uint16(payload[off+4:]), nil
		}
		off += (l + 3) &^ 3
	}
	return 0, fmt.Errorf("没有 %s", name)
}

func nlAttr(t uint16, v []byte) []byte {
	l := 4 + len(v)
	b := make([]byte, (l+3)&^3)
	binary.LittleEndian.PutUint16(b, uint16(l))
	binary.LittleEndian.PutUint16(b[2:], t)
	copy(b[4:], v)
	return b
}

// request 发一条 genetlink 请求（带 ACK），返回 ack 的 error 字段和数据回复的 genl 负载（从 genl 头开始）。
func request(s int, family uint16, cmd uint8, attrs []byte) (int, []byte, error) {
	const hdr = 16
	msg := make([]byte, hdr+4+len(attrs))
	binary.LittleEndian.PutUint32(msg, uint32(len(msg)))
	binary.LittleEndian.PutUint16(msg[4:], family)
	binary.LittleEndian.PutUint16(msg[6:], syscall.NLM_F_REQUEST|syscall.NLM_F_ACK)
	binary.LittleEndian.PutUint32(msg[8:], 1)
	msg[hdr] = cmd
	msg[hdr+1] = 1 // genl version
	copy(msg[hdr+4:], attrs)
	if err := syscall.Sendto(s, msg, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return 0, nil, err
	}
	var payload []byte
	buf := make([]byte, 65536)
	for {
		n, _, err := syscall.Recvfrom(s, buf, 0)
		if err != nil {
			return 0, nil, err
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			return 0, nil, err
		}
		for _, m := range msgs {
			switch m.Header.Type {
			case syscall.NLMSG_ERROR:
				if len(m.Data) < 4 {
					return 0, payload, fmt.Errorf("netlink 回复太短")
				}
				return int(int32(binary.LittleEndian.Uint32(m.Data))), payload, nil
			case syscall.NLMSG_DONE:
				return 0, payload, nil
			default:
				payload = append([]byte(nil), m.Data...)
			}
		}
	}
}
