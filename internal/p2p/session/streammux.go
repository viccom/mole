//go:build p2p

package session

import (
	"context"
	"net"

	"github.com/hashicorp/yamux"
)

// StreamMux 是 session 层依赖的多路复用抽象：把已加密、已可靠的底层 conn（secure.NegotiatedConn）
// 之上开出多条独立 stream（每条都是 net.Conn），供心跳/消息/文件/测速/隧道各自独占一条。
//
// 这是消灭 quic/tcp 双后端的关键：所有传输（UDP-DTLS+KCP / TCP-TLS）在 secure 协商后都产出
// 一个 net.Conn，套上 yamux 后用同一份 session 实现，不再为 quic.Stream 和 TCP 字节流各写一套。
//
// *yamux.Session 原生的 Open()/Accept() 已返回 net.Conn，经 yamuxSession 适配即可满足本接口。
type StreamMux interface {
	// OpenStream 主动开一条新 stream（dialer 侧）。
	OpenStream() (net.Conn, error)
	// AcceptStream 接受一条对端开启的 stream（listener 侧）；在 ctx 取消时返回 ctx.Err()。
	AcceptStream(ctx context.Context) (net.Conn, error)
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Close() error
}

// yamuxSession 把 *yamux.Session 适配为 StreamMux。
type yamuxSession struct{ sess *yamux.Session }

// NewYamuxStreamMux 用一个已建立的 *yamux.Session 构造 StreamMux。
func NewYamuxStreamMux(sess *yamux.Session) StreamMux { return yamuxSession{sess} }

func (y yamuxSession) OpenStream() (net.Conn, error) { return y.sess.Open() }

// AcceptStream 包装 yamux 阻塞的 Accept()，使其响应 ctx 取消（yamux 本身无 AcceptContext）。
func (y yamuxSession) AcceptStream(ctx context.Context) (net.Conn, error) {
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := y.sess.Accept()
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (y yamuxSession) LocalAddr() net.Addr  { return y.sess.LocalAddr() }
func (y yamuxSession) RemoteAddr() net.Addr { return y.sess.RemoteAddr() }
func (y yamuxSession) Close() error         { return y.sess.Close() }
