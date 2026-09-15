//go:build p2p

package session

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"moleAgent_client/internal/p2p/heartbeat"
	"moleAgent_client/internal/p2p/tunnel"
)

// secureSession implements Session over a yamux-multiplexed secure connection
// (replaces quicSession). 所有传输（UDP-DTLS+KCP / TCP-TLS）在 secure 协商后都产出
// 一个 net.Conn，由 engine 层套 yamux 得到 StreamMux，本结构消费它。
//
// Layout: a main bidirectional stream carries text control messages
// (HB / MSG / TUNNEL:) and heartbeats. Separate streams carry file transfer,
// speedtest, and tunnel data — dispatched by a 4-byte magic.
type secureSession struct {
	mux      StreamMux
	isServer bool // mainStream 配对：isServer=Accept，client=Open
	cb       *callbacks
	fileDir  func() string // 动态接收目录；nil/返回"" → CWD（保持原行为）

	mainStream net.Conn
	writeMu    sync.Mutex
	tm         *tunnel.Manager
	ht         *heartbeat.Tracker

	ctx       context.Context
	cancel    context.CancelFunc
	closed    atomic.Bool
	done      chan struct{}
	closeOnce sync.Once
}

func newSecureSession(ctx context.Context, out *Outcome, opts *sessionOpts) (Session, error) {
	if out.Mux == nil {
		return nil, errors.New("secure session: outcome.Mux is nil")
	}
	s := &secureSession{
		mux:      out.Mux,
		isServer: !out.IsClient,
		cb:       &opts.cb,
		fileDir:  opts.fileDir,
		ht:       heartbeat.NewTracker(),
		done:     make(chan struct{}),
	}
	// Derive s.ctx now so CreateTunnel / SendText etc. can use it before Run.
	// Run 复用此 s.ctx（H1：不再替换，避免与 CreateTunnel/Close 并发读 s.ctx 的数据竞争）。
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Open the main control stream synchronously so NewSession returns a
	// fully-usable session. isClient Open / isServer Accept — 两端配对。
	// 计时：mainStream 建立耗时用于排查 yamux keepalive(30s) 与 connected→断(9s) 的时序
	// 矛盾——yamux session 在 secureUpgrade 时创建（keepalive 同时启动），若 AcceptStream
	// 阻塞等对端 SYN 较久，connected 日志会晚于 session 创建，使 keepalive 首次 ping 落在
	// connected 之后 ~9s。
	var err error
	msT0 := time.Now()
	if s.isServer {
		s.mainStream, err = s.mux.AcceptStream(s.ctx)
	} else {
		s.mainStream, err = s.mux.OpenStream()
	}
	if err != nil {
		s.cancel()
		s.mux.Close()
		return nil, fmt.Errorf("secure main stream: %w", err)
	}
	log.Printf("[session secure] main stream ready in %v (isServer=%v)", time.Since(msT0), s.isServer)
	s.mainStream.SetReadDeadline(time.Now().Add(ReadTimeout))

	s.tm = tunnel.NewManager(s.mux, func(msg string) {
		s.writeMu.Lock()
		fmt.Fprintf(s.mainStream, "%s\n", msg)
		s.writeMu.Unlock()
	})

	return s, nil
}

func (s *secureSession) Transport() string { return "secure" }

// ---- Text messaging ----

func (s *secureSession) SendText(text string) error {
	if s.closed.Load() || s.mainStream == nil {
		return ErrSessionClosed
	}
	s.writeMu.Lock()
	_, err := fmt.Fprintf(s.mainStream, "%s%s\n", msgPrefix, text)
	s.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("send text: %w", err)
	}
	return nil
}

func (s *secureSession) OnMessage(cb func(string)) {
	s.cb.mu.Lock()
	s.cb.onMessage = cb
	s.cb.mu.Unlock()
}

// ---- File transfer ----

func (s *secureSession) SendFile(path string) (FileTransferResult, error) {
	if s.closed.Load() {
		return FileTransferResult{}, ErrSessionClosed
	}
	return SendFile(s.mux, path)
}

func (s *secureSession) OnFileReceived(cb func(FileTransferResult)) {
	s.cb.mu.Lock()
	s.cb.onFileReceived = cb
	s.cb.mu.Unlock()
}

// ---- Speedtest ----

func (s *secureSession) RunSpeedtest(sizeMB int) (SpeedtestResult, error) {
	if s.closed.Load() {
		return SpeedtestResult{}, ErrSessionClosed
	}
	if sizeMB < 1 {
		sizeMB = 1
	}
	if sizeMB > 1000 {
		sizeMB = 1000
	}
	return RunSpeedtest(s.mux, sizeMB), nil
}

func (s *secureSession) OnSpeedtestReceived(cb func(SpeedtestResult)) {
	s.cb.mu.Lock()
	s.cb.onSpeedtestRecv = cb
	s.cb.mu.Unlock()
}

// ---- Tunnels ----

func (s *secureSession) CreateTunnel(p tunnel.Params) (TunnelInfo, error) {
	if s.closed.Load() || s.tm == nil {
		return TunnelInfo{}, ErrSessionClosed
	}
	t, err := s.tm.Create(p)
	if err != nil {
		return TunnelInfo{}, fmt.Errorf("tunnel create: %w", err)
	}

	// Wait synchronously for peer's TUNNEL:OK so the returned TunnelInfo refers
	// to a usable tunnel.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if t.Status() == tunnel.StatusActive {
			break
		}
		select {
		case <-s.ctx.Done():
			s.tm.Close(t.ID)
			return TunnelInfo{}, ErrSessionClosed
		case <-time.After(100 * time.Millisecond):
		}
	}
	if t.Status() != tunnel.StatusActive {
		s.tm.Close(t.ID)
		return TunnelInfo{}, errors.New("tunnel activation timeout (peer did not acknowledge)")
	}

	switch p.Protocol {
	case "tcp":
		if err := tunnel.StartTCPListener(t, s.mux); err != nil {
			s.tm.Close(t.ID)
			return TunnelInfo{}, fmt.Errorf("start tcp listener: %w", err)
		}
	case "udp":
		// C4：同步 setup（端口占用等错误不再被 go 吞），再异步 serve
		uln, ust, uerr := tunnel.SetupUDPListener(t, s.mux)
		if uerr != nil {
			s.tm.Close(t.ID)
			return TunnelInfo{}, fmt.Errorf("start udp listener: %w", uerr)
		}
		go tunnel.ServeUDPListener(t, uln, ust)
	default:
		s.tm.Close(t.ID)
		return TunnelInfo{}, fmt.Errorf("unsupported protocol %q", p.Protocol)
	}
	return tunnelInfoFromTunnel(t), nil
}

func (s *secureSession) CloseTunnel(id uint32) error {
	if s.closed.Load() || s.tm == nil {
		return ErrSessionClosed
	}
	if err := s.tm.Close(id); err != nil {
		return fmt.Errorf("tunnel close: %w", err)
	}
	return nil
}

func (s *secureSession) ListTunnels() []TunnelInfo {
	if s.tm == nil {
		return nil
	}
	out := make([]TunnelInfo, 0, 4)
	for _, t := range s.tm.List() {
		out = append(out, tunnelInfoFromTunnel(t))
	}
	return out
}

// ---- Lifecycle ----

func (s *secureSession) Run(_ context.Context) error {
	// H1：不替换 s.ctx——newSecureSession 已用传入 ctx 派生 s.ctx（stable）。
	// Run 内部用 s.ctx（reader/ticker/accept）；Close 调 s.cancel 取消 s.ctx 退出。
	// 原代码 Run 替换 s.ctx 会与 CreateTunnel/Close 并发读 s.ctx 形成数据竞争。
	// NewSession 与 Run 由 desktop 用同一 ctx 调用（connection.go），s.ctx 与本 ctx 同源。
	defer close(s.done)
	defer s.Close()
	defer s.tm.Shutdown()

	var wg sync.WaitGroup
	// streamWG 跟踪 detached per-stream goroutine（HandleFileRX/HandleSpeedtestRX）。
	var streamWG sync.WaitGroup

	// Reader: heartbeat, MSG, TUNNEL: control on main stream.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer s.cancel()
		scanner := bufio.NewScanner(s.mainStream)
		// 允许最大 1MB 行：默认 64KB 上限下，对端发 >64KB 的 MSG 行会触发 ErrTooLong 断连。
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || isProtocolNoise(line) {
				continue
			}
			s.mainStream.SetReadDeadline(time.Now().Add(ReadTimeout))
			s.ht.OnRead()
			if isHeartbeat(line) {
				continue
			}
			if strings.HasPrefix(line, "TUNNEL:") {
				s.tm.HandleControl(line)
				continue
			}
			if strings.HasPrefix(line, msgPrefix) {
				text := strings.TrimPrefix(line, msgPrefix)
				log.Printf("[RECV] %s", text)
				s.cb.fireMessage(text)
			} else {
				log.Printf("[RECV] %s", line)
			}
		}
		// 退出原因诊断：区分 EOF（对端/mux 关闭，scanner.Err()==nil）vs deadline/timeout
		// vs 其他 error。relay 透传 9-10s 断连时 accept stream 报 context canceled / EOF
		// 的根因在此：reader 先退出 → defer cancel → accept goroutine 被取消。
		if e := scanner.Err(); e != nil {
			log.Printf("[session secure] main-stream reader exit: err=%v", e)
		} else {
			log.Printf("[session secure] main-stream reader exit: EOF (peer/mux closed)")
		}
	}()

	// Heartbeat ticker.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 首立即发一个 HB（不等 ticker 首 tick）：session 建立后对端 reader 在空等，
		// 立即发首条 HB 让对端 OnRead + 刷新 ReadDeadline，避免空闲期被 idle timeout
		// 误判断开（relay 透传场景实测 9-10s 断；原 ticker 首 tick 还要 HeartbeatFreq，
		// 加上 ShouldBeat 严格 > 使首 HB 拖到 2×HeartbeatFreq）。
		val := rand.Intn(900000) + 100000
		s.writeMu.Lock()
		_, werr := fmt.Fprintf(s.mainStream, "HB:%d\n", val)
		s.writeMu.Unlock()
		if werr != nil {
			log.Printf("[session secure] heartbeat(initial) write failed: %v", werr)
		}
		s.ht.OnWrite()
		ticker := time.NewTicker(HeartbeatFreq)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if s.ht.ShouldBeat(HeartbeatFreq) {
					val := rand.Intn(900000) + 100000
					s.writeMu.Lock()
					_, werr := fmt.Fprintf(s.mainStream, "HB:%d\n", val)
					s.writeMu.Unlock()
					if werr != nil {
						log.Printf("[session secure] heartbeat write failed: %v", werr)
					}
					s.ht.OnWrite()
				}
			}
		}
	}()

	// Incoming stream accept: file / speedtest / tunnel-data.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer s.cancel()
		for {
			st, e := s.mux.AcceptStream(s.ctx)
			if e != nil {
				log.Printf("[session secure] accept stream: %v", e)
				return
			}
			// 首 4 字节 magic 必须限时：同步 ReadFull 无 deadline 时，
			// 一条只开不写的 stream 即可挂死整个 accept 循环（全部入站服务停摆）
			st.SetReadDeadline(time.Now().Add(streamHeaderTimeout))
			header := make([]byte, 4)
			if _, e := io.ReadFull(st, header); e != nil {
				log.Printf("[session secure] stream header: %v", e)
				st.Close()
				continue
			}
			st.SetReadDeadline(time.Time{})
			magic := binary.BigEndian.Uint32(header)
			// dispatch 统一异步分发入站流：
			//  1. streamWG 登记，Run 结尾等它们收尾；
			//  2. 处理函数内部没有读 deadline，对端「写半截就停」会永久阻塞——
			//     而 Run 结尾的 streamWG.Wait() 会因此永远等不到，Run 不返回，
			//     上层 handler 的 <-runDone 卡死（该隧道从此不再重连）。会话 ctx
			//     结束时用过期 deadline 打断这些阻塞读；
			//  3. closeStream=true 的分支（文件/测速）收尾关流：接收侧只读不关
			//     会让 yamux streams 表无界增长。隧道数据流不可在此关——其
			//     HandleDataStream 内部另起 goroutine 后立即返回，流由 Manager 接管。
			dispatch := func(st net.Conn, closeStream bool, handle func()) {
				streamWG.Add(1)
				go func() {
					defer streamWG.Done()
					if closeStream {
						defer st.Close()
					}
					stopWatch := make(chan struct{})
					defer close(stopWatch)
					go func() {
						select {
						case <-s.ctx.Done():
							_ = st.SetReadDeadline(time.Now())
						case <-stopWatch:
						}
					}()
					handle()
				}()
			}
			switch magic {
			case FileMagic:
				dispatch(st, true, func() {
					dir := ""
					if s.fileDir != nil {
						dir = s.fileDir() // 快照读，调用方 mu 保证并发安全（R11）
					}
					HandleFileRX(st, dir, func(name, savePath string, size int64, elapsed time.Duration) {
						s.cb.fireFile(FileTransferResult{FileName: name, Path: savePath, Size: size, Elapsed: elapsed})
					})
				})
			case tunnel.TCPMagic, tunnel.UDPMagic:
				// H-NEW-1：异步分发（与 File/Speedtest 一致），避免 HandleDataStream 同步读
				// tunnelID 阻塞 accept loop（buggy peer 写一半停会挂死整个 session）
				dispatch(st, false, func() { s.tm.HandleDataStream(magic, st) })
			default:
				dispatch(st, true, func() {
					HandleSpeedtestRX(st, header, func(r SpeedtestResult) {
						s.cb.fireSpeedtest(r)
					})
				})
			}
		}
	}()

	wg.Wait()
	streamWG.Wait()
	if err := s.ctx.Err(); err != nil {
		return fmt.Errorf("session ctx: %w", err)
	}
	return nil
}

func (s *secureSession) Close() error {
	s.closeOnce.Do(func() {
		log.Printf("[session secure] Close: session shutting down")
		s.closed.Store(true)
		if s.cancel != nil {
			s.cancel()
		}
		if s.mainStream != nil {
			s.mainStream.Close()
		}
		// mux.Close 关闭 yamux session + 底层 NegotiatedConn（级联关 secure 层 + 打洞 socket）。
		s.mux.Close()
	})
	return nil
}

func (s *secureSession) Done() <-chan struct{} { return s.done }

// tunnelInfoFromTunnel copies the public fields of a *tunnel.Tunnel into a
// lock-free TunnelInfo. Stats 经 atomic.Uint64.Load 读取（addIn/addOut 用 .Add 写）——
// atomic.Uint64 保证 32 位平台 8 字节对齐，避免 unaligned atomic panic。
func tunnelInfoFromTunnel(t *tunnel.Tunnel) TunnelInfo {
	return TunnelInfo{
		ID: t.ID, Label: t.StringID(), Protocol: t.Params.Protocol,
		LocalPort: t.Params.LocalPort, TargetHost: t.Params.TargetHost, TargetPort: t.Params.TargetPort,
		BytesIn:  t.Stats.BytesIn.Load(),
		BytesOut: t.Stats.BytesOut.Load(),
	}
}
