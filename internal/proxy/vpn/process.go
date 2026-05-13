package vpn

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ProcessMgr 进程管理器
type ProcessMgr struct {
	mu      sync.RWMutex
	name    string
	cfg     Config
	cmd     *exec.Cmd
	process *os.Process

	// 状态
	running     bool
	startTime   time.Time
	crashCount  int
	crashLogs   []CrashLog

	// 启动失败诊断
	lastError      string
	lastErrorPhase string
	lastErrorTime  int64

	// vnt-cli REST API
	vntClient *VNTClient

	// 日志
	logBuf    *circularBuffer
	logWriter io.WriteCloser

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// 全局进程管理器映射（用于互斥检查）
var globalManagers = struct {
	mu      sync.RWMutex
	procs   map[string]*ProcessMgr
}{
	procs: make(map[string]*ProcessMgr),
}

// NewProcessMgr 创建进程管理器
func NewProcessMgr(name string, cfg Config) (*ProcessMgr, error) {
	// 互斥检查：同名程序只能一个实例
	globalManagers.mu.RLock()
	if _, ok := globalManagers.procs[name]; ok {
		globalManagers.mu.RUnlock()
		return nil, fmt.Errorf("vpn manager %q already exists", name)
	}
	globalManagers.mu.RUnlock()

	// 初始化日志缓冲区
	var logBuf io.Writer
	var logWriter io.WriteCloser
	if cfg.Log.Capture {
		logBuf = newCircularBuffer(cfg.Log.MaxSize)
		if cfg.Log.OutputPath != "" {
			if err := os.MkdirAll(filepath.Dir(cfg.Log.OutputPath), 0755); err != nil {
				return nil, fmt.Errorf("create log dir: %w", err)
			}
			f, err := os.OpenFile(cfg.Log.OutputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return nil, fmt.Errorf("open log file: %w", err)
			}
			logWriter = f
		}
	}

	pm := &ProcessMgr{
		name:      name,
		cfg:       cfg,
		logWriter: logWriter,
	}
	if cfg.Log.Capture {
		pm.logBuf = logBuf.(*circularBuffer)
	}

	// 注册到全局映射
	globalManagers.mu.Lock()
	globalManagers.procs[name] = pm
	globalManagers.mu.Unlock()

	return pm, nil
}

// Start 启动进程
func (pm *ProcessMgr) Start(ctx context.Context) error {
	pm.mu.Lock()
	if pm.running {
		pm.mu.Unlock()
		return fmt.Errorf("process already running")
	}
	pm.ctx, pm.cancel = context.WithCancel(ctx)
	pm.running = true
	pm.clearError()
	pm.mu.Unlock()

	// 查找程序路径
	binPath, err := pm.findBinary()
	if err != nil {
		pm.setError("binary", err.Error())
		pm.Stop()
		return err
	}

	// 创建命令
	cmd := exec.Command(binPath, pm.cfg.BuildArgs()...)
	cmd.SysProcAttr = getSysProcAttr()

	// 设置工作目录
	if dir := filepath.Dir(binPath); dir != "" {
		cmd.Dir = dir
	}

	pm.mu.Lock()
	pm.cmd = cmd
	pm.mu.Unlock()

	// 启动进程
	if err := pm.cmd.Start(); err != nil {
		pm.setError("startup", err.Error())
		pm.Stop()
		return fmt.Errorf("start process: %w", err)
	}

	pm.mu.Lock()
	pm.process = pm.cmd.Process
	pm.startTime = time.Now()
	pm.mu.Unlock()

	// 初始化 vnt-cli REST 客户端
	if pm.cfg.VNT != nil && pm.cfg.VNT.Enabled {
		port := pm.cfg.VNT.RestPort
		if port <= 0 {
			port = DefaultRestPort
		}
		pm.mu.Lock()
		pm.vntClient = NewVNTClient(port)
		pm.mu.Unlock()
		go pm.healthProbe()
	}

	// 如果需要捕获输出
	if pm.cfg.Log.Capture {
		if stdout, err := pm.cmd.StdoutPipe(); err == nil {
			go pm.copyOutput(stdout)
		}
		if stderr, err := pm.cmd.StderrPipe(); err == nil {
			go pm.copyOutput(stderr)
		}
	}

	// 等待进程退出
	pm.wg.Add(1)
	go func() {
		defer pm.wg.Done()
		err := pm.cmd.Wait()
		pm.handleExit(err)
	}()

	log.Printf("vpn-manager %s started (pid: %d)", pm.name, pm.process.Pid)
	return nil
}

// Stop 停止进程
func (pm *ProcessMgr) Stop() {
	pm.mu.Lock()
	if !pm.running {
		pm.mu.Unlock()
		return
	}
	pm.running = false
	if pm.cancel != nil {
		pm.cancel()
	}
	pm.mu.Unlock()

	// 尝试优雅退出
	if pm.process != nil {
		// 使用平台相关的优雅停止方式
		// Windows: 使用 Kill (由于 CREATE_NEW_PROCESS_GROUP)
		// Unix/Linux/macOS: 使用 SIGTERM
		if err := StopProcess(pm.process); err != nil {
			log.Printf("vpn-manager %s stop warning: %v", pm.name, err)
		}

		// 等待退出超时
		done := make(chan struct{})
		go func() {
			pm.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(time.Duration(pm.cfg.Watchdog.QuitGrace) * time.Second):
			// 超时，强制 kill
			log.Printf("vpn-manager %s timeout, force kill", pm.name)
			pm.process.Kill()
		}
	}

	// 关闭日志文件
	if pm.logWriter != nil {
		pm.logWriter.Close()
	}

	// 从全局映射移除
	globalManagers.mu.Lock()
	delete(globalManagers.procs, pm.name)
	globalManagers.mu.Unlock()

	log.Printf("vpn-manager %s stopped", pm.name)
}

// IsRunning 检查是否运行中
func (pm *ProcessMgr) IsRunning() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.running && pm.process != nil
}

// Status 返回状态
func (pm *ProcessMgr) Status() Status {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	status := Status{
		Name:        pm.name,
		Running:     pm.running,
		CrashCount:  pm.crashCount,
		CrashLogs:   make([]CrashLog, len(pm.crashLogs)),
		Error:       pm.lastError,
		ErrorPhase:  pm.lastErrorPhase,
		ErrorTime:   pm.lastErrorTime,
	}
	copy(status.CrashLogs, pm.crashLogs)

	if pm.process != nil {
		status.PID = pm.process.Pid
	}
	if !pm.startTime.IsZero() {
		status.StartTime = pm.startTime.UnixMilli()
	}

	if pm.vntClient != nil {
		restPort := DefaultRestPort
		if pm.cfg.VNT != nil && pm.cfg.VNT.RestPort > 0 {
			restPort = pm.cfg.VNT.RestPort
		}
		status.RestPort = restPort
	}

	return status
}

// CrashLogs 返回崩溃日志
func (pm *ProcessMgr) CrashLogs() []CrashLog {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]CrashLog, len(pm.crashLogs))
	copy(result, pm.crashLogs)
	return result
}

// findBinary 查找程序路径
func (pm *ProcessMgr) findBinary() (string, error) {
	binName := pm.cfg.Binary.Name
	if binName == "" {
		return "", fmt.Errorf("binary name is required")
	}

	// 1. 如果指定了绝对路径
	if pm.cfg.Binary.Path != "" {
		if _, err := os.Stat(pm.cfg.Binary.Path); err == nil {
			return pm.cfg.Binary.Path, nil
		}
	}

	// 2. 在 ./vnet/ 目录下查找
	vnetDir := "./vnet"
	if exePath, err := os.Executable(); err == nil {
		vnetDir = filepath.Join(filepath.Dir(exePath), "vnet")
	}
	if path := filepath.Join(vnetDir, binName); !strings.HasPrefix(binName, "/") {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		// Windows 下尝试 .exe 后缀
		if strings.ToLower(filepath.Ext(binName)) != ".exe" {
			exePath := path + ".exe"
			if _, err := os.Stat(exePath); err == nil {
				return exePath, nil
			}
		}
	}

	// 3. 在 $PATH 中查找
	if path, err := exec.LookPath(binName); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("binary %q not found in ./vnet/ or $PATH", binName)
}

// setError 设置启动失败诊断信息
func (pm *ProcessMgr) setError(phase, msg string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.lastError = msg
	pm.lastErrorPhase = phase
	pm.lastErrorTime = time.Now().UnixMilli()
}

// clearError 清除错误信息
func (pm *ProcessMgr) clearError() {
	pm.lastError = ""
	pm.lastErrorPhase = ""
	pm.lastErrorTime = 0
}

// healthProbe 后台探针：检查 vnt-cli REST API 是否可达
func (pm *ProcessMgr) healthProbe() {
	consecutiveOK := 0
	for {
		select {
		case <-pm.ctx.Done():
			return
		default:
		}

		pm.mu.RLock()
		client := pm.vntClient
		pm.mu.RUnlock()

		if client == nil {
			return
		}

		_, err := client.Status()
		if err == nil {
			consecutiveOK++
			if consecutiveOK == 1 {
				pm.mu.Lock()
				pm.clearError()
				pm.mu.Unlock()
			}
		} else {
			if consecutiveOK == 0 {
				pm.mu.Lock()
				pm.lastError = "REST API 不可达: " + err.Error()
				pm.lastErrorPhase = "api"
				pm.lastErrorTime = time.Now().UnixMilli()
				pm.mu.Unlock()
			}
			consecutiveOK = 0
		}

		time.Sleep(2 * time.Second)
	}
}

// VNTData 查询 vnt-cli REST API 获取实时数据
func (pm *ProcessMgr) VNTData() (*VNTInfo, []VNTDeviceItem, []VNTRouteItem, *VNTBuildInfo) {
	pm.mu.RLock()
	client := pm.vntClient
	pm.mu.RUnlock()

	if client == nil {
		return nil, nil, nil, nil
	}

	info, _ := client.Info()
	peers, _ := client.List()
	routes, _ := client.Route()
	status, _ := client.Status()
	return info, peers, routes, status
}

// VNTChart 查询 vnt-cli 流量统计
func (pm *ProcessMgr) VNTChart() (*VNTChartA, error) {
	pm.mu.RLock()
	client := pm.vntClient
	pm.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("vnt client not available")
	}
	return client.Chart()
}

// handleExit 处理进程退出
func (pm *ProcessMgr) handleExit(err error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if !pm.running {
		return // 正常停止
	}

	// 记录崩溃
	logTail := []byte{}
	if pm.logBuf != nil {
		logTail = pm.logBuf.Bytes()
	}
	crash := CrashLog{
		Timestamp: time.Now().UnixMilli(),
		LogTail:   logTail,
	}

	var exitCode int
	var sig syscall.Signal = -1

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			if s, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				sig = syscall.Signal(s.Signal())
				crash.Signal = int(sig)
			}
		}
	}
	crash.ExitCode = exitCode
	crash.ID = fmt.Sprintf("crash-%d", crash.Timestamp)
	pm.crashLogs = append(pm.crashLogs, crash)
	pm.crashCount++

	// 设置崩溃错误信息
	pm.lastError = fmt.Sprintf("exit code %d", exitCode)
	if sig >= 0 {
		pm.lastError = fmt.Sprintf("signal %v", sig)
	}
	pm.lastErrorPhase = "crash"
	pm.lastErrorTime = time.Now().UnixMilli()

	log.Printf("vpn-manager %s crashed (exit: %d, signal: %v, crash #%d)",
		pm.name, exitCode, sig, pm.crashCount)

	// 检查是否需要自动重启
	if pm.cfg.Lifecycle.RestartOnCrash && pm.crashCount <= pm.cfg.Lifecycle.MaxRestarts {
		pm.mu.Unlock()
		time.Sleep(time.Duration(pm.cfg.Lifecycle.RestartDelay) * time.Second)
		pm.mu.Lock()

		// 重新启动
		pm.process = nil
		pm.mu.Unlock()
		pm.Start(pm.ctx)
		pm.mu.Lock()
	} else if pm.crashCount > pm.cfg.Lifecycle.MaxRestarts {
		log.Printf("vpn-manager %s reached max restarts (%d), stopping",
			pm.name, pm.cfg.Lifecycle.MaxRestarts)
		pm.running = false
	}
}

// copyOutput 复制进程输出到日志缓冲区
func (pm *ProcessMgr) copyOutput(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if pm.logBuf != nil {
				pm.logBuf.Write(buf[:n])
			}
			if pm.logWriter != nil {
				pm.logWriter.Write(buf[:n])
			}
		}
		if err != nil {
			break
		}
	}
}

// circularBuffer 环形缓冲区
type circularBuffer struct {
	buf    []byte
	size   int
	write  int
	mu     sync.Mutex
}

func newCircularBuffer(size int) *circularBuffer {
	return &circularBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

func (cb *circularBuffer) Write(p []byte) (n int, err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	for len(p) > 0 {
		remain := cb.size - cb.write
		if remain == 0 {
			cb.write = 0
			remain = cb.size
		}
		toWrite := len(p)
		if toWrite > remain {
			toWrite = remain
		}
		copy(cb.buf[cb.write:], p[:toWrite])
		cb.write += toWrite
		p = p[toWrite:]
		n += toWrite
	}
	return n, nil
}

func (cb *circularBuffer) Bytes() []byte {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.write < cb.size {
		return cb.buf[:cb.write]
	}
	// 返回最后 size 字节
	result := make([]byte, cb.size)
	copy(result, cb.buf[cb.write-cb.size:cb.write])
	return result
}
