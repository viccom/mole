package tunnel

import (
	"bufio"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"

	"moleAgent_Serv/internal/core"
)

// claimUDPSession 语义（REL-03）：持锁把新建会话回插 sessions；
// 同 key 已有并发创建者时，新会话是败者——不落 map，返回既有会话，
// 由调用方负责关闭败者资源（cancel/读协程退出/配额释放）
func TestClaimUDPSession(t *testing.T) {
	mu := &sync.Mutex{}
	sessions := make(map[string]*udpSession)
	winner := &udpSession{}
	loser := &udpSession{}

	// 空 map：直接插入并胜出
	got, inserted := claimUDPSession(mu, sessions, "k", winner)
	if !inserted || got != winner {
		t.Fatalf("first insert must win, inserted=%v got=%p", inserted, got)
	}
	if sessions["k"] != winner {
		t.Fatal("winner must be in map")
	}

	// 预置 key 后并发插入：败者不落 map，返回既有会话供转发
	got, inserted = claimUDPSession(mu, sessions, "k", loser)
	if inserted || got != winner {
		t.Fatalf("late inserter must lose, inserted=%v got=%p", inserted, got)
	}
	if sessions["k"] != winner {
		t.Fatal("map must keep original winner")
	}

	// 同一指针重复声明：幂等胜出（已占据者重入不误判为败者）
	got, inserted = claimUDPSession(mu, sessions, "k", winner)
	if !inserted || got != winner {
		t.Fatalf("re-claim of the same session must be idempotent, inserted=%v", inserted)
	}
}

// ===== REL-04：UDP 会话并发信号量 =====

// startUDPEchoNode 构造模拟节点：对每条新建流先读掉隧道标识头行，
// 之后把数据原样回显（扮演客户端节点的 UDP 转发）
func startUDPEchoNode(t *testing.T, mp *mockNodeProvider, nodeID string) {
	t.Helper()
	pipeSrv, pipeCli := net.Pipe()
	srvSess, err := smux.Server(pipeSrv, nil)
	if err != nil {
		t.Fatalf("smux server: %v", err)
	}
	cliSess, err := smux.Client(pipeCli, nil)
	if err != nil {
		t.Fatalf("smux client: %v", err)
	}
	mp.sessions[nodeID] = srvSess
	t.Cleanup(func() { cliSess.Close(); srvSess.Close() })
	go func() {
		for {
			stream, err := cliSess.AcceptStream()
			if err != nil {
				return
			}
			go func(s *smux.Stream) {
				defer s.Close()
				br := bufio.NewReader(s)
				if _, err := br.ReadString('\n'); err != nil {
					return
				}
				buf := make([]byte, 65535)
				for {
					n, err := br.Read(buf)
					if n > 0 {
						if _, werr := s.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(stream)
		}
	}()
}

// freeUDPPort 探测一个当前空闲的 UDP 端口
func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatalf("probe free udp port: %v", err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// UDP 会话占用网关并发信号量（与 TCP 连接同语义）：额度耗尽时新会话
// 的首个包被丢弃；隧道停止后所有会话销毁，额度必须全额归还（防泄漏）
func TestUDPSessionSemaphoreAndRelease(t *testing.T) {
	const nodeID = "Node0001"
	const tunnelName = "udp-sem"

	port := freeUDPPort(t)
	mp := newMockNodeProvider()
	mp.addNode(&core.Node{
		ID:     nodeID,
		Name:   nodeID,
		Status: core.NodeStatusOnline,
		Tunnels: []core.Tunnel{
			{Name: tunnelName, Type: core.TunnelTypeUDP, Target: "127.0.0.1:53", ListenPort: port},
		},
	})
	startUDPEchoNode(t, mp, nodeID)

	tg := NewTunnelGateway(mp, 1, nil) // sem 容量 1
	if err := tg.StartUDP(context.Background(), core.Tunnel{
		Name: tunnelName, Type: core.TunnelTypeUDP, Target: "127.0.0.1:53", ListenPort: port,
	}); err != nil {
		t.Fatalf("StartUDP: %v", err)
	}

	dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	sockA, err := net.DialUDP("udp", nil, dst)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	defer sockA.Close()
	sockB, err := net.DialUDP("udp", nil, dst)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer sockB.Close()

	// 第一个源地址：会话创建成功（占用唯一额度）并收到回显
	msgA := []byte("hello-from-a")
	if _, err := sockA.Write(msgA); err != nil {
		t.Fatalf("write A: %v", err)
	}
	sockA.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 128)
	n, err := sockA.Read(buf)
	if err != nil {
		t.Fatalf("A should get echo: %v", err)
	}
	if string(buf[:n]) != string(msgA) {
		t.Fatalf("echo mismatch: got %q", buf[:n])
	}
	// 会话存活期间占用一个额度（len(ch) 即已占用数）
	if got := len(tg.sem.ch); got != 1 {
		t.Fatalf("active session must hold one semaphore slot, in-use=%d", got)
	}

	// 第二个源地址：额度已被 A 占满，新会话被拒——本包丢弃，无任何响应
	if _, err := sockB.Write([]byte("hello-from-b")); err != nil {
		t.Fatalf("write B: %v", err)
	}
	sockB.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
	if n, err := sockB.Read(buf); err == nil {
		t.Fatalf("B must be dropped while semaphore exhausted, got %q", buf[:n])
	}

	// 停止隧道：所有会话销毁（StopTunnel 同步等待运行循环退出），
	// 并发额度必须全额归还
	tg.StopTunnel(tunnelName)
	if got := len(tg.sem.ch); got != 0 {
		t.Fatalf("semaphore must be fully released after tunnel stop, in-use=%d", got)
	}
}
