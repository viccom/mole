package vpn

import (
	"strconv"
	"strings"
)

// Config vpn-manager 隧道配置
type Config struct {
	Binary BinaryConfig `json:"binary"`
	Args   []string     `json:"args"`

	Lifecycle LifecycleConfig `json:"lifecycle"`
	Watchdog  WatchdogConfig  `json:"watchdog"`
	Log       LogConfig       `json:"log"`

	VNT *VNTConfig `json:"vnt,omitempty"`
}

// VNTConfig vnt-cli 专用配置
type VNTConfig struct {
	Enabled  bool   `json:"enabled"`   // 是否为 vnt-cli 程序（启用后激活 REST API 集成）
	Token    string `json:"token"`     // -k 连接令牌
	Server   string `json:"server"`    // -s VPN 服务器地址
	DeviceID string `json:"device_id"` // -d 设备标识
	Name     string `json:"name"`      // -n 设备名称
	Password string `json:"password"`  // -w 密码
	InIP     string `json:"in_ip"`     // -i 输入代理子网
	OutIP    string `json:"out_ip"`    // -o 输出代理子网
	IP       string `json:"ip"`        // --ip 指定虚拟IP
	RestPort int    `json:"rest_port"` // REST API 端口，默认 59871
}

// BinaryConfig 程序配置
type BinaryConfig struct {
	Name string `json:"name"` // 可执行文件名，如 "easytier-core"
	Path string `json:"path"` // 绝对路径，为空则在 ./vnet/ 和 $PATH 中查找
}

// LifecycleConfig 生命周期配置
type LifecycleConfig struct {
	Autostart      bool `json:"autostart"`        // moleAgent 启动时自动启动
	RestartOnCrash bool `json:"restart_on_crash"` // 崩溃后自动重启
	MaxRestarts    int  `json:"max_restarts"`     // 最大重启次数，默认 3
	RestartDelay   int  `json:"restart_delay"`    // 重启延迟(秒)，默认 5
}

// WatchdogConfig 监视配置
type WatchdogConfig struct {
	Enabled   bool `json:"enabled"`    // 启用进程监视
	Interval  int  `json:"interval"`   // 检查间隔(秒)，默认 10
	QuitGrace int  `json:"quit_grace"` // 优雅退出超时(秒)，默认 10
}

// LogConfig 日志配置
type LogConfig struct {
	Capture    bool   `json:"capture"`     // 捕获进程输出
	MaxSize    int    `json:"max_size"`    // 日志缓冲区大小，默认 64KB
	OutputPath string `json:"output_path"` // 日志写入文件，为空则内存缓冲
}

// CrashLog 崩溃日志
type CrashLog struct {
	ID        string `json:"id"`
	Timestamp int64  `json:"timestamp"`
	ExitCode  int    `json:"exit_code"`
	Signal    int    `json:"signal"`
	LogTail   []byte `json:"log_tail"`
}

// Status 状态
type Status struct {
	Name       string     `json:"name"`
	Running    bool       `json:"running"`
	CrashCount int        `json:"crash_count"`
	PID        int        `json:"pid,omitempty"`
	StartTime  int64      `json:"start_time,omitempty"`
	CrashLogs  []CrashLog `json:"crash_logs"`

	// 启动失败诊断
	Error      string `json:"error,omitempty"`
	ErrorPhase string `json:"error_phase,omitempty"` // binary / startup / crash / api
	ErrorTime  int64  `json:"error_time,omitempty"`

	// vnt-cli REST API 数据
	RestPort  int             `json:"rest_port,omitempty"`
	VNTInfo   *VNTInfo        `json:"vnt_info,omitempty"`
	VNTPeers  []VNTDeviceItem `json:"vnt_peers,omitempty"`
	VNTRoutes []VNTRouteItem  `json:"vnt_routes,omitempty"`
	VNTStatus *VNTBuildInfo   `json:"vnt_status,omitempty"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		Lifecycle: LifecycleConfig{
			Autostart:      false,
			RestartOnCrash: true,
			MaxRestarts:    3,
			RestartDelay:   5,
		},
		Watchdog: WatchdogConfig{
			Enabled:   true,
			Interval:  10,
			QuitGrace: 10,
		},
		Log: LogConfig{
			Capture: true,
			MaxSize: 65536,
		},
	}
}

const DefaultRestPort = 59871

// BuildArgs 根据 VNT 配置动态构建启动参数，优先于静态 Args
func (c *Config) BuildArgs() []string {
	if c.VNT != nil && c.VNT.Enabled {
		args := filterUnmanagedArgs(c.Args)
		if c.VNT.Token != "" {
			args = append(args, "-k", c.VNT.Token)
		}
		if c.VNT.Server != "" {
			args = append(args, "-s", c.VNT.Server)
		}
		if c.VNT.DeviceID != "" {
			args = append(args, "-d", c.VNT.DeviceID)
		}
		if c.VNT.Name != "" {
			args = append(args, "-n", c.VNT.Name)
		}
		if c.VNT.Password != "" {
			args = append(args, "-w", c.VNT.Password)
		}
		if c.VNT.InIP != "" {
			args = append(args, "-i", c.VNT.InIP)
		}
		if c.VNT.OutIP != "" {
			args = append(args, "-o", c.VNT.OutIP)
		}
		if c.VNT.IP != "" {
			args = append(args, "--ip", c.VNT.IP)
		}
		if c.VNT.RestPort > 0 {
			args = append(args, "--rest-port", strconv.Itoa(c.VNT.RestPort))
		}
		return args
	}
	return c.Args
}

func filterUnmanagedArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if isManagedVNTFlag(args[i]) {
			if i+1 < len(args) {
				i++
			}
			continue
		}
		result = append(result, args[i])
	}
	return result
}

func isManagedVNTFlag(arg string) bool {
	switch arg {
	case "-k", "-s", "-d", "-n", "-w", "-i", "-o", "--ip", "--rest-port":
		return true
	default:
		return false
	}
}

// sensitiveVNTFlags 值必须脱敏的 vnt-cli 参数：-k 连接令牌、-w 密码。
// 新增敏感参数必须同时登记在此（与服务端/前端共享的 vnt-cli 契约）
var sensitiveVNTFlags = map[string]bool{"-k": true, "-w": true}

// redactedArgs 返回脱敏后的参数副本，供日志使用。
//
// 绝不修改入参：args 同时是 exec.Command 的真实 argv，就地改写会把
// <redacted> 真的传给子进程。按「标志-值对」投影而非子串替换——令牌值
// 恰好等于其他参数值时既不误伤也不漏伤；连写形式 "-k=SECRET" 整 token 置换。
//
// 同时覆盖非 VNT 路径：BuildArgs 在 VNT 未启用时原样返回 c.Args，其中
// 可能含手写的 -k/-w（filterUnmanagedArgs 只作用于 VNT 分支）。
func redactedArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// 连写形式（复核轮发现）："-k=SECRET" / "-w=SECRET" 不带独立值位，
		// 标志-值对投影会漏掉，须整 token 置换
		if strings.HasPrefix(arg, "-k=") || strings.HasPrefix(arg, "-w=") {
			out = append(out, arg[:3]+"<redacted>")
			continue
		}
		out = append(out, arg)
		if sensitiveVNTFlags[arg] && i+1 < len(args) {
			i++
			out = append(out, "<redacted>")
		}
	}
	return out
}
