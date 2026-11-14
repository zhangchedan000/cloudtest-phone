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
