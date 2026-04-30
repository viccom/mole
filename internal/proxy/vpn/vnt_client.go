package vpn

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// VNTClient 封装对 vnt-cli REST API 的调用
type VNTClient struct {
	baseURL string
	client  *http.Client
}

// NewVNTClient 创建 vnt-cli REST API 客户端
func NewVNTClient(port int) *VNTClient {
	return &VNTClient{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func (c *VNTClient) get(path string, v any) error {
	resp, err := c.client.Get(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("vnt api %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("vnt api %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// Info 返回本机设备信息
func (c *VNTClient) Info() (*VNTInfo, error) {
	var info VNTInfo
	if err := c.get("/info", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// List 返回在线设备列表
func (c *VNTClient) List() ([]VNTDeviceItem, error) {
	var list []VNTDeviceItem
	if err := c.get("/list", &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Status 返回版本/CPU/内存
func (c *VNTClient) Status() (*VNTBuildInfo, error) {
	var info VNTBuildInfo
	if err := c.get("/status", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Route 返回路由表
func (c *VNTClient) Route() ([]VNTRouteItem, error) {
	var list []VNTRouteItem
	if err := c.get("/route", &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Chart 返回总流量统计
func (c *VNTClient) Chart() (*VNTChartA, error) {
	var chart VNTChartA
	if err := c.get("/chart", &chart); err != nil {
		return nil, err
	}
	return &chart, nil
}

// VNTInfo 对应 vnt-cli GET /info 响应
type VNTInfo struct {
	Name            string   `json:"name"`
	VirtualIP       string   `json:"virtual_ip"`
	VirtualGateway  string   `json:"virtual_gateway"`
	VirtualNetmask  string   `json:"virtual_netmask"`
	ConnectStatus   string   `json:"connect_status"`
	RelayServer     string   `json:"relay_server"`
	NATType         string   `json:"nat_type"`
	PublicIPs       string   `json:"public_ips"`
	LocalAddr       string   `json:"local_addr"`
	IPv6Addr        string   `json:"ipv6_addr"`
	UDPListenAddr   []string `json:"udp_listen_addr"`
	TCPListenAddr   string   `json:"tcp_listen_addr"`
}

// VNTDeviceItem 对应 vnt-cli GET /list 中的设备项
type VNTDeviceItem struct {
	Name               string `json:"name"`
	VirtualIP          string `json:"virtual_ip"`
	NATType            string `json:"nat_type"`
	PublicIPs          string `json:"public_ips"`
	LocalIP            string `json:"local_ip"`
	IPv6               string `json:"ipv6"`
	NATTraversalType   string `json:"nat_traversal_type"`
	RT                 string `json:"rt"`
	Status             string `json:"status"`
	ClientSecret       bool   `json:"client_secret"`
	WireGuard          bool   `json:"wire_guard"`
}

// VNTRouteItem 对应 vnt-cli GET /route 中的路由项
type VNTRouteItem struct {
	Destination string `json:"destination"`
	NextHop     string `json:"next_hop"`
	Metric      string `json:"metric"`
	RT          string `json:"rt"`
	Interface   string `json:"interface"`
}

// VNTBuildInfo 对应 vnt-cli GET /status 响应
type VNTBuildInfo struct {
	Version     string  `json:"version"`
	GitTag      string  `json:"git_tag"`
	GitHash     string  `json:"git_hash"`
	BuildTime   string  `json:"build_time"`
	Serial      string  `json:"serial"`
	ExePath     string  `json:"exe_path"`
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage uint64  `json:"memory_usage"`
}

// VNTChartA 对应 vnt-cli GET /chart 响应
type VNTChartA struct {
	DisableStats bool               `json:"disable_stats"`
	UpTotal      uint64             `json:"up_total"`
	DownTotal    uint64             `json:"down_total"`
	UpMap        map[string]uint64  `json:"up_map"`
	DownMap      map[string]uint64  `json:"down_map"`
}
