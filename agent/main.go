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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zhangchedan000/prism-phone/agent/internal/api"
	"github.com/zhangchedan000/prism-phone/agent/internal/assets"
	"github.com/zhangchedan000/prism-phone/agent/internal/docker"
	netcfg "github.com/zhangchedan000/prism-phone/agent/internal/network"
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
	gappsImages := flag.String("gapps-images", "", "带谷歌服务（GMS）的镜像：安卓版本=镜像，逗号分隔；空 = 这台机器不支持谷歌服务。镜像用 host/build-gapps-image.sh 生成")
	network := flag.String("network", "pp-net", "容器网络名")
	subnet := flag.String("subnet", "10.88.0.0/16", "容器网段")
	memMB := flag.Int64("mem-mb", 3072, "每台容器内存上限（MB）")
	cpus := flag.Float64("cpus", 2, "每台容器 CPU 上限")
	maxRunning := flag.Int("max-running", 4, "同时最多运行几台")
	devHost := flag.Bool("dev-host", false, "开发机：允许「直连（仅开发）」代理")
	dockerSock := flag.String("docker", "/var/run/docker.sock", "Docker socket")
	mihomoAPI := flag.String("mihomo-api", "", "网络模块：mihomo 控制接口地址（例如 127.0.0.1:9090）；空 = 不开网络模块（只适合开发测试）")
	tproxyPort := flag.Int("tproxy-port", 7893, "网络模块：mihomo 透明代理端口")
	dnsListen := flag.String("dns-listen", "10.88.0.1:1053", "网络模块：mihomo DNS 监听地址（容器网关）")
	checkPort := flag.Int("check-port", 7899, "网络模块：检测入口端口（只监听 127.0.0.1，用来检测 vless / trojan 等节点；0 = 不开）")
	trashDays := flag.Int("trash-days", 30, "回收站里的旧数据保留几天，超过自动彻底删除（0 = 不自动删）")
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
	gappsImgs, err := parseImages(*gappsImages)
	if err != nil {
		log.Fatalf("参数 -gapps-images 不对：%v", err)
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
		Images: imgs, GappsImages: gappsImgs, DataDir: *dataDir, Network: *network, Subnet: *subnet, MemoryMB: *memMB, CPUs: *cpus,
		MaxRunning: *maxRunning, DevHost: *devHost,
	}
	svc := phones.NewService(cfg, dc, st, cat, models)
	var net *netcfg.Syncer
	if *mihomoAPI != "" {
		if err := svc.EnsureNetwork(context.Background()); err != nil {
			log.Printf("网络模块：建容器网络失败：%v", err)
		}
		net = &netcfg.Syncer{
			Path: filepath.Join(*dataDir, "mihomo", "config.yaml"),
			Opt:  netcfg.Options{TProxyPort: *tproxyPort, DNSListen: *dnsListen, Subnet: *subnet, DevHost: *devHost, APIListen: *mihomoAPI, CheckPort: *checkPort},
		}
		syncNet := func() {
			if err := net.Sync(context.Background(), st.Snapshot()); err != nil {
				log.Printf("网络模块：%v", err)
			}
		}
		syncNet()
		go func() {
			for range time.Tick(3 * time.Second) {
				syncNet()
			}
		}()
		svc.SetNetworkGuard(func(id string) error {
			syncNet()
			if err := net.LastError(); err != nil {
				return err
			}
			if why, ok := net.Blocked()[id]; ok {
				return fmt.Errorf("没有网络出口：%s", why)
			}
			return nil
		})
	} else {
		log.Printf("网络模块没开（-mihomo-api 为空）：云手机直接用宿主机网络，只能用来开发测试")
	}
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
	go svc.RunUsageSampler(context.Background(), 10*time.Second)
	// 上网健康检查：每 2 分钟从每台运行中的手机试连外网（踩坑 #34 断网一个小时才被发现）
	go svc.RunInternetChecker(context.Background(), 2*time.Minute)
	// 电池漂移：每 3 分钟把每台手机的电量/温度推一小步，别让电池冻在一个固定值（battery.go）
	go svc.RunBatteryDrift(context.Background(), 3*time.Minute)
	var health *netcfg.HealthChecker
	if net != nil {
		// 网络模块的宿主机检查：策略路由被删了 30 秒内自动补回
		health = &netcfg.HealthChecker{Syncer: net}
		go health.Loop(context.Background(), 30*time.Second)
	}
	if *trashDays > 0 {
		go func() {
			for {
				if n, err := svc.PurgeTrash(time.Duration(*trashDays) * 24 * time.Hour); err != nil {
					log.Printf("清理回收站：%v", err)
				} else if n > 0 {
					log.Printf("回收站：彻底删除了 %d 份超过 %d 天的旧数据", n, *trashDays)
				}
				time.Sleep(6 * time.Hour)
			}
		}()
	}
	apiServer := api.New(svc, st, webFS, token, check)
	if net != nil && net.CanCheck() {
		apiServer.SetNetworkCheck(net.Check)
	}
	svc.SetProxyChecker(func(ctx context.Context, id string) error {
		_, err := apiServer.RunProxyCheck(ctx, id)
		return err
	})
	// 管家重启时正在开机的手机：开机后设置 / 伪造文件会被中断，这里补做（踩坑 #45）
	if n := svc.ResumeRunning(context.Background()); n > 0 {
		log.Printf("补做 %d 台运行中手机的开机后设置", n)
	}
	if health != nil {
		apiServer.SetNetworkHealth(func() any {
			if l := health.Last(); l != nil {
				return l
			}
			return nil
		})
	}
	srv := &http.Server{Addr: *listen, Handler: apiServer.Handler(), ReadHeaderTimeout: 10 * time.Second}
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
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
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
