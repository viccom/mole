package vpn

// Config vpn-manager 隧道配置
type Config struct {
	Binary BinaryConfig `json:"binary"`
	Args   []string     `json:"args"`

	Lifecycle LifecycleConfig `json:"lifecycle"`
	Watchdog  WatchdogConfig  `json:"watchdog"`
	Log       LogConfig       `json:"log"`
}

// BinaryConfig 程序配置
type BinaryConfig struct {
	Name string `json:"name"` // 可执行文件名，如 "easytier-core"
	Path string `json:"path"` // 绝对路径，为空则在 ./vnet/ 和 $PATH 中查找
}

// LifecycleConfig 生命周期配置
type LifecycleConfig struct {
	Autostart      bool `json:"autostart"`       // moleAgent 启动时自动启动
	RestartOnCrash bool `json:"restart_on_crash"` // 崩溃后自动重启
	MaxRestarts    int  `json:"max_restarts"`    // 最大重启次数，默认 3
	RestartDelay   int  `json:"restart_delay"`   // 重启延迟(秒)，默认 5
}

// WatchdogConfig 监视配置
type WatchdogConfig struct {
	Enabled    bool `json:"enabled"`     // 启用进程监视
	Interval   int  `json:"interval"`     // 检查间隔(秒)，默认 10
	QuitGrace  int  `json:"quit_grace"`   // 优雅退出超时(秒)，默认 10
}

// LogConfig 日志配置
type LogConfig struct {
	Capture    bool   `json:"capture"`     // 捕获进程输出
	MaxSize    int    `json:"max_size"`     // 日志缓冲区大小，默认 64KB
	OutputPath string `json:"output_path"`  // 日志写入文件，为空则内存缓冲
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
	Name        string     `json:"name"`
	Running     bool       `json:"running"`
	CrashCount  int        `json:"crash_count"`
	PID         int        `json:"pid,omitempty"`
	StartTime   int64      `json:"start_time,omitempty"`
	CrashLogs   []CrashLog `json:"crash_logs"`
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
