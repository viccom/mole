// Package stun 提供极简内嵌 STUN 服务器（仅应答 Binding Request），
// 作为 P2P 客户端 NAT 探测的兜底（公共 STUN 不可达时）。
// 默认关闭，由配置 stun.enabled 门控，无 build tag。
package stun

import (
	"errors"
	"fmt"
	"net"

	"github.com/pion/stun/v3"
)

// Server STUN Binding 应答器
type Server struct {
	conn *net.UDPConn
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

// ListenAndServe 阻塞应答 Binding Request，直到 Close。非 STUN / 非 Binding 报文忽略。
func (s *Server) ListenAndServe() error {
	buf := make([]byte, 1500)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("read udp: %w", err)
		}
		m := &stun.Message{Raw: buf[:n]}
		if err := m.Decode(); err != nil || m.Type != stun.BindingRequest {
			continue
		}
		// 归一化 IPv4（4 字节），IPv6 保持 16 字节；XORMappedAddress.AddTo 按长度选择 family
		ip := from.IP
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		// 顺序敏感：事务 ID 必须先于 XORMappedAddress 设置（XOR 掩码取自 msg.TransactionID）
		resp, err := stun.Build(
			stun.BindingSuccess,
			stun.NewTransactionIDSetter(m.TransactionID),
			stun.XORMappedAddress{IP: ip, Port: from.Port},
		)
		if err != nil {
			continue
		}
		if _, err := s.conn.WriteToUDP(resp.Raw, from); err != nil {
			return fmt.Errorf("write udp: %w", err)
		}
	}
}
