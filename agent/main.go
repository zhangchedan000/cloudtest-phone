// prism-phone-agent：每台 ARM 机器上跑一个，管理这台机器上的安卓容器，并提供管理页。
//
// 配置都从命令行参数来（板子相关的放在 boards/<板子>/agent.env，systemd 服务读它）。
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/api"
	"github.com/zhangchedan000/prism-phone/agent/internal/assets"
	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
	"github.com/zhangchedan000/prism-phone/agent/internal/phones"
	"github.com/zhangchedan000/prism-phone/agent/internal/selfcheck"
	"github.com/zhangchedan000/prism-phone/agent/internal/store"
)

var version = "dev"

func main() {
	listen := flag.String("listen", "127.0.0.1:8099", "监听地址（默认只监听本机，外部访问走 HTTPS 反向代理或 SSH 隧道）")
	dataDir := flag.String("data", "/opt/prism-phone/data", "数据目录")
	hostID := flag.String("host-id", hostname(), "机器标识")
	hostName := flag.String("host-name", "", "机器显示名")
	gpuFamily := flag.String("gpu-family", "software", "显卡家族：software（无显卡）/ Mali-G610 …")
	gpuMode := flag.String("gpu-mode", "guest", "Redroid gpu_mode：guest（软件渲染）/ host")
	images := flag.String("images", "14=redroid/redroid:14.0.0_64only-latest", "安卓版本=镜像，逗号分隔")
	network := flag.String("network", "pp-net", "容器网络名")
	subnet := flag.String("subnet", "10.88.0.0/16", "容器网段")
	memMB := flag.Int64("mem-mb", 3072, "每台容器内存上限（MB）")
	cpus := flag.Float64("cpus", 2, "每台容器 CPU 上限")
	maxRunning := flag.Int("max-running", 4, "同时最多运行几台")
	devHost := flag.Bool("dev-host", false, "开发机：允许「直连（仅开发）」代理")
	dockerSock := flag.String("docker", "/var/run/docker.sock", "Docker socket")
	showVersion := flag.Bool("version", false, "显示版本")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	token := os.Getenv("PP_TOKEN")

	imgs, err := parseImages(*images)
	if err != nil {
		log.Fatalf("参数 -images 不对：%v", err)
	}
	profilesFS, err := fs.Sub(assets.FS, "profiles")
	if err != nil {
		log.Fatal(err)
	}
	cat, models, err := phones.LoadProfiles(profilesFS, ".")
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	dc := docker.New(*dockerSock)
	cfg := phones.Config{
		HostID: *hostID, HostName: firstNonEmpty(*hostName, *hostID), GPUFamily: *gpuFamily, GPUMode: *gpuMode,
		Images: imgs, DataDir: *dataDir, Network: *network, Subnet: *subnet, MemoryMB: *memMB, CPUs: *cpus,
		MaxRunning: *maxRunning, DevHost: *devHost,
	}
	svc := phones.NewService(cfg, dc, st, cat, models)
	check := func() selfcheck.Report { return selfcheck.Run(*dataDir, dc.Ping) }
	rep := check()
	for _, it := range rep.Items {
		mark := "✓"
		if !it.OK {
			mark = "✗ " + it.Fix
		}
		log.Printf("自检 %-10s %s %s", it.Name, it.Info, mark)
	}
	if !rep.OK {
		log.Printf("自检有问题，管理页能打开，但开机可能失败")
	}
	if token == "" && !strings.HasPrefix(*listen, "127.0.0.1:") && !strings.HasPrefix(*listen, "localhost:") {
		log.Fatal("监听非本机地址时必须设置 PP_TOKEN（防止别人直接控制你的云手机）")
	}

	webFS, err := fs.Sub(assets.FS, "web")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: *listen, Handler: api.New(svc, st, webFS, token, check).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("prism-phone-agent %s 监听 %s（机器 %s，显卡 %s，机型 %d 个）", version, *listen, cfg.HostName, cfg.GPUFamily, len(models))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func parseImages(s string) (map[int]string, error) {
	out := map[int]string{}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("%q 应该是 版本=镜像", part)
		}
		v, err := strconv.Atoi(kv[0])
		if err != nil {
			return nil, err
		}
		out[v] = kv[1]
	}
	return out, nil
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
