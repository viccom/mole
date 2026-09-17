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
	running    bool
	startTime  time.Time
	crashCount int
	crashLogs  []CrashLog

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

	// gen 启动代际：每次 Start 递增，用于淘汰上一代的后台探针
	// （崩溃重启直接复用 Start，旧探针若不淘汰会永久泄漏）
	gen int

	// lifecycleMu 串行化「崩溃迟到的重启」与 Stop：running 检查与 Start
	// 之间不再给 Stop 留插入窗口（否则产生无人持有的孤儿进程）
	lifecycleMu sync.Mutex
}

// 全局进程管理器映射（用于互斥检查）
var globalManagers = struct {
	mu    sync.RWMutex
	procs map[string]*ProcessMgr
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

// 消费点默认值：服务端存储的 Para 可能缺省生命周期/日志字段（零值），
// 直接使用会导致"开了重启却永不重启 / 0 宽限强杀 / 环形缓冲死循环"
const (
	defaultMaxRestarts   = 3
	defaultQuitGrace     = 10
	defaultLogBufferSize = 64 << 10
)

// Start 启动进程（经 lifecycleMu 与 Stop/迟到重启串行化）
func (pm *ProcessMgr) Start(ctx context.Context) error {
	pm.lifecycleMu.Lock()
	defer pm.lifecycleMu.Unlock()
	return pm.startLocked(ctx)
}

// rollbackStart 回滚启动失败：startLocked 持有 lifecycleMu，直接调 Stop 会
// 二次加锁自死锁（sync.Mutex 不可重入）——此处做与 Stop 等价的收尾（进程没
// 起来，无需杀进程/等 wg），并注销全局表，否则同名实例再也无法重建。
func (pm *ProcessMgr) rollbackStart() {
	pm.mu.Lock()
	pm.running = false
	if pm.cancel != nil {
		pm.cancel()
	}
	logWriter := pm.logWriter
	pm.logWriter = nil
	pm.mu.Unlock()
	if logWriter != nil {
		logWriter.Close()
	}
	globalManagers.mu.Lock()
	delete(globalManagers.procs, pm.name)
	globalManagers.mu.Unlock()
}

// ensureLogWriter 重开被上次 Stop 关闭的日志文件：ProcessMgr 实例在 Manager
// 的 procs 表中被复用（stop → start / tunnel_action: start），Stop 关闭文件时
// 置 nil，重启路径据此重开，否则 copyOutput 一直写已关闭的 fd，文件日志静默失效。
func (pm *ProcessMgr) ensureLogWriter(cfg Config) {
	if !cfg.Log.Capture || cfg.Log.OutputPath == "" {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.logWriter != nil {
		return
	}
	f, err := os.OpenFile(cfg.Log.OutputPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("vpn-manager: reopen log file for %s failed: %v", pm.name, err)
		return
	}
	pm.logWriter = f
}

// startLocked 要求已持 lifecycleMu：handleExit 的迟到重启路径在本锁
// 持有状态下调用，改走 Start 会自死锁（sync.Mutex 不可重入）
func (pm *ProcessMgr) startLocked(ctx context.Context) error {
	pm.mu.Lock()
	if pm.running {
		pm.mu.Unlock()
		return fmt.Errorf("process already running")
	}
	pm.ctx, pm.cancel = context.WithCancel(ctx)
	pm.gen++
	gen := pm.gen
	pm.running = true
	cfg := pm.cfg // 快照：OnTunnelUpdate 可能并发改写 pm.cfg（持 pm.mu 写入）
	pm.clearError()
	pm.mu.Unlock()

	log.Printf("vpn-manager: starting %s", pm.name)

	// 重新打开被上次 Stop 关闭的日志文件（实例由 Manager 复用，详见 ensureLogWriter）
	pm.ensureLogWriter(cfg)

	// 查找程序路径
	binPath, err := pm.findBinary(cfg)
	if err != nil {
		log.Printf("vpn-manager: binary not found for %s: %v", pm.name, err)
		pm.setError("binary", err.Error())
		pm.rollbackStart()
		return err
	}
	log.Printf("vpn-manager: found binary at %s", binPath)

	// 构建命令参数
	args := cfg.BuildArgs()
	log.Printf("vpn-manager: command: %s %v", binPath, args)

	// 创建命令
	cmd := exec.Command(binPath, args...)
	cmd.SysProcAttr = getSysProcAttr()

	// 设置工作目录
	if dir := filepath.Dir(binPath); dir != "" {
		cmd.Dir = dir
		log.Printf("vpn-manager: working directory: %s", cmd.Dir)
	}

	pm.mu.Lock()
	pm.cmd = cmd
	pm.mu.Unlock()

	// 日志管道必须在 Start() 之前创建：Start 之后调用恒报
	// "exec: StdoutPipe after process started"，导致日志捕获整体失效
	var stdoutPipe, stderrPipe io.ReadCloser
	if cfg.Log.Capture {
		stdoutPipe, _ = cmd.StdoutPipe()
		stderrPipe, _ = cmd.StderrPipe()
	}

	// 启动进程
	log.Printf("vpn-manager: launching process for %s", pm.name)
	if err := pm.cmd.Start(); err != nil {
		log.Printf("vpn-manager: failed to start process for %s: %v", pm.name, err)
		pm.setError("startup", err.Error())
		// 启动失败已建的管道无写入端，随回滚一并关闭（否则 fd 泄漏）
		if stdoutPipe != nil {
			stdoutPipe.Close()
		}
		if stderrPipe != nil {
			stderrPipe.Close()
		}
		pm.rollbackStart()
		return fmt.Errorf("start process: %w", err)
	}

	pm.mu.Lock()
	pm.process = pm.cmd.Process
	pm.startTime = time.Now()
	pm.mu.Unlock()

	// 初始化 vnt-cli REST 客户端
	if cfg.VNT != nil && cfg.VNT.Enabled {
		port := cfg.VNT.RestPort
		if port <= 0 {
			port = DefaultRestPort
		}
		pm.mu.Lock()
		pm.vntClient = NewVNTClient(port)
		pm.mu.Unlock()
		go pm.healthProbe(pm.ctx, gen)
	}

	// 等待进程退出
	pm.wg.Add(1)
	go func() {
		defer pm.wg.Done()
		err := pm.cmd.Wait()
		pm.handleExit(err)
	}()

	// 输出复制在 Start 之后启动（管道在此之前没有写入端，Read 会阻塞）
	if stdoutPipe != nil {
		go pm.copyOutput(stdoutPipe)
	}
	if stderrPipe != nil {
		go pm.copyOutput(stderrPipe)
	}

	log.Printf("vpn-manager %s started (pid: %d)", pm.name, cmd.Process.Pid)
	return nil
}

// Stop 停止进程
func (pm *ProcessMgr) Stop() {
	// lifecycleMu 只护住 running 翻转这一小段（wg.Wait 绝不持它，
	// 否则与等待本锁的 handleExit 互锁）：保证迟到的崩溃重启要么
	// 在本翻转前完成（Stop 随后能拿到新进程引用并杀掉），要么被
	// running 检查挡下
	pm.lifecycleMu.Lock()
	pm.mu.Lock()
	if !pm.running {
		pm.mu.Unlock()
		pm.lifecycleMu.Unlock()
		return
	}
	pm.running = false
	if pm.cancel != nil {
		pm.cancel()
	}
	pm.mu.Unlock()
	pm.lifecycleMu.Unlock()

	// 尝试优雅退出
	// pm.process 由 handleExit 重启路径持锁置 nil，必须持锁快照（与 Start 的写入同步）
	pm.mu.Lock()
	process := pm.process
	quitGrace := pm.cfg.Watchdog.QuitGrace
	if quitGrace <= 0 {
		quitGrace = defaultQuitGrace // 0 宽限 = SIGTERM 后立即强杀
	}
	pm.mu.Unlock()

	if process != nil {
		// 使用平台相关的优雅停止方式
		// Windows: 使用 Kill (由于 CREATE_NEW_PROCESS_GROUP)
		// Unix/Linux/macOS: 使用 SIGTERM
		if err := StopProcess(process); err != nil {
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
		case <-time.After(time.Duration(quitGrace) * time.Second):
			// 超时，强制 kill
			log.Printf("vpn-manager %s timeout, force kill", pm.name)
			process.Kill()
		}
	}

	// 关闭日志文件（同时置 nil：实例会被 Manager 复用重启，
	// startLocked 的 ensureLogWriter 以 nil 为「需要重开」的信号）
	pm.mu.Lock()
	logWriter := pm.logWriter
	pm.logWriter = nil
	pm.mu.Unlock()
	if logWriter != nil {
		logWriter.Close()
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
		Name:       pm.name,
		Running:    pm.running,
		CrashCount: pm.crashCount,
		CrashLogs:  make([]CrashLog, len(pm.crashLogs)),
		Error:      pm.lastError,
		ErrorPhase: pm.lastErrorPhase,
		ErrorTime:  pm.lastErrorTime,
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

// findBinary 查找程序路径（cfg 由调用方在 pm.mu 下快照后传入：
// startLocked 执行期间 OnTunnelUpdate 可能并发改写 pm.cfg）
func (pm *ProcessMgr) findBinary(cfg Config) (string, error) {
	binName := cfg.Binary.Name
	if binName == "" {
		return "", fmt.Errorf("binary name is required")
	}

	// 1. 如果指定了绝对路径
	if cfg.Binary.Path != "" {
		if _, err := os.Stat(cfg.Binary.Path); err == nil {
			return cfg.Binary.Path, nil
		}
	}

	// 2. 在 ./vnt/ 目录下查找
	vntDir := "./vnt"
	if exePath, err := os.Executable(); err == nil {
		vntDir = filepath.Join(filepath.Dir(exePath), "vnt")
	}
	log.Printf("vpn-manager: looking for %q in %s", binName, vntDir)
	if path := filepath.Join(vntDir, binName); !strings.HasPrefix(binName, "/") {
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

	return "", fmt.Errorf("binary %q not found in ./vnt/ or $PATH", binName)
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

// healthProbe 后台探针：检查 vnt-cli REST API 是否可达。
// ctx 为所属代际的 ctx；gen 与当前代际不符（已被新一轮 Start 取代）时退出，
// 防止崩溃重启后旧探针永久泄漏
func (pm *ProcessMgr) healthProbe(ctx context.Context, gen int) {
	consecutiveOK := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pm.mu.RLock()
		client := pm.vntClient
		currentGen := pm.gen
		pm.mu.RUnlock()

		if client == nil || currentGen != gen {
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

	// 检查是否需要自动重启（Para 缺省 max_restarts 时为 0，
	// 会让 restart_on_crash=true 一次都不重启，回退到默认值）
	maxRestarts := pm.cfg.Lifecycle.MaxRestarts
	if maxRestarts <= 0 {
		maxRestarts = defaultMaxRestarts
	}
	if pm.cfg.Lifecycle.RestartOnCrash && pm.crashCount <= maxRestarts {
		restartDelay := pm.cfg.Lifecycle.RestartDelay // 快照：解锁后不再裸读 pm.cfg
		pm.mu.Unlock()
		time.Sleep(time.Duration(restartDelay) * time.Second)
		// 迟到重启与 Stop 互斥：睡眠期间 Stop 翻转了 running 则放弃复活；
		// 本检查与 Start 之间也不再给 Stop 留插入窗口
		pm.lifecycleMu.Lock()
		pm.mu.Lock()

		if !pm.running {
			// 只释放 lifecycleMu：pm.mu 由函数入口的 defer 解锁，
			// 此处再显式解锁会触发 fatal error: unlock of unlocked mutex
			pm.lifecycleMu.Unlock()
			return
		}

		// 复位 running：崩溃路径中 running 从未翻转，而 startLocked 的幂等
		// 守卫会拒绝 running==true 的调用——不复位则自动重启 100% 静默失败
		pm.running = false
		pm.process = nil
		pm.mu.Unlock()
		if err := pm.startLocked(pm.ctx); err != nil {
			log.Printf("vpn-manager: crash restart failed for %s: %v", pm.name, err)
		}
		pm.lifecycleMu.Unlock()
		pm.mu.Lock()
	} else if pm.crashCount > maxRestarts {
		log.Printf("vpn-manager %s reached max restarts (%d), stopping",
			pm.name, pm.cfg.Lifecycle.MaxRestarts)
		pm.running = false
	}
}

// copyOutput 复制进程输出到日志缓冲区
func (pm *ProcessMgr) copyOutput(r io.Reader) {
	// 启动时快照日志文件：Stop 会并发把字段置 nil 并关闭文件，
	// 裸读字段与写入构成数据竞争（写已关闭 fd 由本函数忽略错误）
	pm.mu.RLock()
	logWriter := pm.logWriter
	pm.mu.RUnlock()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if pm.logBuf != nil {
				pm.logBuf.Write(buf[:n])
			}
			if logWriter != nil {
				logWriter.Write(buf[:n])
			}
		}
		if err != nil {
			break
		}
	}
}

// circularBuffer 环形缓冲区
type circularBuffer struct {
	buf   []byte
	size  int
	write int
	full  bool // 是否已环绕（环绕后 write<size 不代表未写满）
	mu    sync.Mutex
}

func newCircularBuffer(size int) *circularBuffer {
	if size <= 0 {
		// Para 缺省 max_size 时为 0：size=0 会让 Write 死循环（toWrite 恒为 0）
		size = defaultLogBufferSize
	}
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
			cb.full = true
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

	if !cb.full {
		return cb.buf[:cb.write]
	}
	// 环绕后按逻辑顺序线性化：旧段 [write:] 在前，新段 [:write] 在后
	result := make([]byte, 0, cb.size)
	result = append(result, cb.buf[cb.write:]...)
	result = append(result, cb.buf[:cb.write]...)
	return result
}
