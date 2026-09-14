// Package stun 提供极简内嵌 STUN 服务器（仅应答 Binding Request），
// 作为 P2P 客户端 NAT 探测的兜底（公共 STUN 不可达时）。
// 默认关闭，由配置 stun.enabled 门控，无 build tag。
package stun

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/pion/stun/v3"
)

// Server STUN Binding 应答器
type Server struct {
	// conn 抽象为 PacketConn：生产为 *net.UDPConn，测试可注入故障 mock
	conn net.PacketConn
}

// NewServer 绑定 bindAddr（如 ":3478"）
func NewServer(bindAddr string) (*Server, error) {
	addr, err := net.ResolveUDPAddr("udp", bindAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve udp addr %q: %w", bindAddr, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen udp %q: %w", bindAddr, err)
	}
	return &Server{conn: conn}, nil
}

// LocalAddr 返回实际绑定地址（支持 ":0" 随机端口）
func (s *Server) LocalAddr() net.Addr { return s.conn.LocalAddr() }

// Close 关闭监听，ListenAndServe 随之返回
func (s *Server) Close() error { return s.conn.Close() }

// stunErrBackoff 连续错误退避：100ms 起指数增长、5s 封顶。固定 10Hz 重试在
// 持久性故障下会以约 86 万行/天刷日志且无升级信号（复审 R4）。
func stunErrBackoff(consecutive int) time.Duration {
	d := 100 * time.Millisecond << uint(min(consecutive-1, 6))
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// ListenAndServe 阻塞应答 Binding Request，直到 Close。非 STUN / 非 Binding 报文忽略。
//
// 瞬时读/写错误（接口抖动、ENOBUFS、ICMP 偶发反馈）只记日志并继续——STUN 是
// 可选兜底组件，绝不允许它终止服务。读侧按连续错误次数指数退避（5s 封顶），
// 防持久性故障热烧 CPU 与刷日志（复审 R4）。
func (s *Server) ListenAndServe() error {
	buf := make([]byte, 1500)
	consecutiveErrs := 0
	for {
		n, from, err := s.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			consecutiveErrs++
			slog.Warn("STUN read failed, retrying", "error", err, "consecutive", consecutiveErrs)
			time.Sleep(stunErrBackoff(consecutiveErrs))
			continue
		}
		consecutiveErrs = 0
		m := &stun.Message{Raw: buf[:n]}
		if err := m.Decode(); err != nil || m.Type != stun.BindingRequest {
			continue
		}
		udpFrom, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		// 归一化 IPv4（4 字节），IPv6 保持 16 字节；XORMappedAddress.AddTo 按长度选择 family
		ip := udpFrom.IP
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		// 顺序敏感：事务 ID 必须先于 XORMappedAddress 设置（XOR 掩码取自 msg.TransactionID）
		resp, err := stun.Build(
			stun.BindingSuccess,
			stun.NewTransactionIDSetter(m.TransactionID),
			stun.XORMappedAddress{IP: ip, Port: udpFrom.Port},
		)
		if err != nil {
			continue
		}
		if _, err := s.conn.WriteTo(resp.Raw, from); err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			slog.Warn("STUN write failed, dropping response", "error", err)
			continue
		}
	}
}
