// Package docker 是最小的 Docker Engine API 客户端（只用标准库，通过 unix socket）。
// 只实现管家程序用到的几个接口，方便在任何 ARM 机器上直接编译。
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	http *http.Client
}

func New(socket string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &Client{http: &http.Client{Transport: transport, Timeout: 60 * time.Second}}
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker %d: %s", e.Status, e.Message) }

func IsNotFound(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	// 不写死接口版本：Docker 29 要求 ≥1.44，旧版本 Docker 又不认新版本号，不带版本号就用守护进程自己的版本。
	u := "http://docker" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &msg)
		if msg.Message == "" {
			msg.Message = string(data)
		}
		return &APIError{Status: resp.StatusCode, Message: msg.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ContainerSpec 管家程序创建容器需要的全部参数。
type ContainerSpec struct {
	Name       string
	Image      string
	Cmd        []string
	Labels     map[string]string
	Binds      []string
	MemoryMB   int64
	NanoCPUs   int64
	Network    string
	IPv4       string
	Privileged bool
	// MacAddress 网卡 MAC（空 = Docker 自己分配 02:42:…，一看就是 Docker，踩坑 #130）。
	MacAddress string
	// Entrypoint 不为空时替换镜像的入口（开机时长归零的启动器用，见 timens 包）。
	Entrypoint []string
}

func (c *Client) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	body := map[string]any{
		"Image":  spec.Image,
		"Cmd":    spec.Cmd,
		"Labels": spec.Labels,
		"HostConfig": map[string]any{
			"Privileged":    spec.Privileged,
			"Binds":         spec.Binds,
			"Memory":        spec.MemoryMB * 1024 * 1024,
			"MemorySwap":    spec.MemoryMB * 1024 * 1024,
			"NanoCpus":      spec.NanoCPUs,
			"NetworkMode":   spec.Network,
			"RestartPolicy": map[string]any{"Name": "no"},
		},
		"NetworkingConfig": map[string]any{
			"EndpointsConfig": map[string]any{
				spec.Network: map[string]any{"IPAMConfig": map[string]any{"IPv4Address": spec.IPv4}},
			},
		},
	}
	if len(spec.Entrypoint) > 0 {
		body["Entrypoint"] = spec.Entrypoint
	}
	if spec.MacAddress != "" {
		// API 1.44 起在网络端点里设；老版本认容器配置里的。两处写同一个值，新老 Docker 都生效。
		body["MacAddress"] = spec.MacAddress
		ep := body["NetworkingConfig"].(map[string]any)["EndpointsConfig"].(map[string]any)[spec.Network].(map[string]any)
		ep["MacAddress"] = spec.MacAddress
	}
	var out struct {
		ID string `json:"Id"`
	}
	err := c.do(ctx, http.MethodPost, "/containers/create", url.Values{"name": {spec.Name}}, body, &out)
	return out.ID, err
}

func (c *Client) StartContainer(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+name+"/start", nil, nil, nil)
}

func (c *Client) StopContainer(ctx context.Context, name string, timeoutSec int) error {
	return c.do(ctx, http.MethodPost, "/containers/"+name+"/stop", url.Values{"t": {fmt.Sprint(timeoutSec)}}, nil, nil)
}

func (c *Client) RemoveContainer(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/containers/"+name, url.Values{"force": {"true"}}, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

type ContainerState struct {
	Running   bool   `json:"Running"`
	Status    string `json:"Status"`
	StartedAt string `json:"StartedAt"`
	ExitCode  int    `json:"ExitCode"`
	Pid       int    `json:"Pid"`
}

type ContainerInfo struct {
	Name   string         `json:"Name"`
	State  ContainerState `json:"State"`
	Config struct {
		Cmd    []string          `json:"Cmd"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func (c *Client) InspectContainer(ctx context.Context, name string) (*ContainerInfo, error) {
	var out ContainerInfo
	if err := c.do(ctx, http.MethodGet, "/containers/"+name+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ImageEntrypoint 镜像原来的入口（Redroid 是 /init + 几个参数），换成启动器时要原样接在后面。
func (c *Client) ImageEntrypoint(ctx context.Context, image string) ([]string, error) {
	var out struct {
		Config struct {
			Entrypoint []string `json:"Entrypoint"`
		} `json:"Config"`
	}
	if err := c.do(ctx, http.MethodGet, "/images/"+image+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Config.Entrypoint, nil
}

// EnsureNetwork 确保有一个固定网段的桥接网络（每台云手机在里面有固定 IP）。
func (c *Client) EnsureNetwork(ctx context.Context, name, subnet, gateway string) error {
	err := c.do(ctx, http.MethodGet, "/networks/"+name, nil, nil, nil)
	if err == nil {
		return nil
	}
	if !IsNotFound(err) {
		return err
	}
	body := map[string]any{
		"Name":   name,
		"Driver": "bridge",
		"IPAM":   map[string]any{"Config": []map[string]string{{"Subnet": subnet, "Gateway": gateway}}},
		"Options": map[string]string{
			"com.docker.network.bridge.name": name,
		},
		"EnableIPv6": false,
		"Labels":     map[string]string{"prism-phone": "1"},
	}
	return c.do(ctx, http.MethodPost, "/networks/create", nil, body, nil)
}

func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

func (c *Client) ImageExists(ctx context.Context, image string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/images/"+url.PathEscape(image)+"/json", nil, nil, nil)
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// Usage 一个容器的资源占用。
type Usage struct {
	CPUPercent float64 `json:"cpuPercent"` // 占整台机器的百分比（4 核满载 = 100）
	MemMB      float64 `json:"memMB"`
	MemLimitMB float64 `json:"memLimitMB"`
}

type statsResponse struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

// UsageFromStats 按 docker stats 的算法算 CPU 百分比和内存（内存扣掉文件缓存）。
func UsageFromStats(raw []byte) (Usage, error) {
	var s statsResponse
	if err := json.Unmarshal(raw, &s); err != nil {
		return Usage{}, err
	}
	var u Usage
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemCPUUsage) - float64(s.PreCPUStats.SystemCPUUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		u.CPUPercent = cpuDelta / sysDelta * 100
	}
	mem := s.MemoryStats.Usage
	if cache, ok := s.MemoryStats.Stats["inactive_file"]; ok && cache < mem {
		mem -= cache
	}
	u.MemMB = float64(mem) / (1 << 20)
	u.MemLimitMB = float64(s.MemoryStats.Limit) / (1 << 20)
	return u, nil
}

// ContainerUsage 读一次容器的资源占用（Docker 会采样约 1 秒）。
func (c *Client) ContainerUsage(ctx context.Context, name string) (Usage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/containers/"+name+"/stats", url.Values{"stream": {"false"}}, nil, &raw); err != nil {
		return Usage{}, err
	}
	u, err := UsageFromStats(raw)
	if err == nil && u.MemLimitMB == 0 {
		// 手机走虚拟 WiFi 时 eth0 被搬出容器，Docker 读网卡统计失败，整份统计都是 0（踩坑 #57）：直接读 cgroup
		var id struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &id) == nil && id.ID != "" {
			if cu, cerr := CgroupUsage(ctx, CgroupRoot, id.ID); cerr == nil {
				return cu, nil
			}
		}
	}
	return u, err
}
