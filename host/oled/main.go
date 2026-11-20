// pp-oled: 点亮魔云腾 Q1 正面的 SSD1306 128x64 I2C 小屏（/dev/i2c-3, 0x3C），
// 显示本机 IP / 在线手机数 / CPU 温度 / 内存。读 /mmc/pp-data/oled-off 标志熄屏（开关）。
// 用 periph.io 驱动（SSD1306 标准屏）。板子上 OpenRC 自启（见 host/board/etc/init.d/prism-phone-oled）。
package main

import (
	"fmt"
	"image"
	"image/color"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/devices/v3/ssd1306"
	"periph.io/x/host/v3"
)

// 开关标志：这个文件存在 = 熄屏（管家 API / 命令都能 touch/rm 它）。
const offFlag = "/mmc/pp-data/oled-off"

func main() {
	if _, err := host.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "host init:", err)
		os.Exit(1)
	}
	bus, err := i2creg.Open("3") // /dev/i2c-3
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开 i2c-3:", err)
		os.Exit(1)
	}
	defer bus.Close()
	dev, err := ssd1306.NewI2C(bus, &ssd1306.DefaultOpts) // 128x64 @ 0x3C
	if err != nil {
		fmt.Fprintln(os.Stderr, "ssd1306:", err)
		os.Exit(1)
	}

	blank := image.NewGray(image.Rect(0, 0, 128, 64))
	off := false
	for {
		if fileExists(offFlag) {
			if !off { // 刚被关：清屏一次
				_ = dev.Draw(blank.Bounds(), blank, image.Point{})
				off = true
			}
			time.Sleep(2 * time.Second)
			continue
		}
		off = false
		_ = dev.Draw(blank.Bounds(), renderScreen(), image.Point{})
		time.Sleep(3 * time.Second)
	}
}

func renderScreen() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, 128, 64))
	lines := []string{
		"PrismPhone",
		"IP " + localIP(),
		"Phones:" + phoneCount() + "  " + cpuTemp() + "C",
		"Mem " + memUsed(),
	}
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.Gray{Y: 255}), Face: basicfont.Face7x13}
	y := 12
	for _, ln := range lines {
		d.Dot = fixed.P(0, y)
		d.DrawString(ln)
		y += 15
	}
	return img
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func localIP() string {
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Name != "eth0" {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "-"
}

func cpuTemp() string {
	b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return "-"
	}
	t, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return strconv.Itoa(t / 1000)
}

func memUsed() string {
	b, _ := os.ReadFile("/proc/meminfo")
	var total, avail int
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) >= 2 {
			v, _ := strconv.Atoi(f[1])
			switch f[0] {
			case "MemTotal:":
				total = v
			case "MemAvailable:":
				avail = v
			}
		}
	}
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f/%.0fG", float64(total-avail)/1048576, float64(total)/1048576)
}

func phoneCount() string {
	out, err := exec.Command("docker", "ps", "-q", "--filter", "label=prism-phone").Output()
	if err != nil {
		return "?"
	}
	return strconv.Itoa(len(strings.Fields(string(out))))
}
