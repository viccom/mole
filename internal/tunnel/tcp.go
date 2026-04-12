package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"moleAgent_Serv/internal/core"
)

// StartTCP 启动 TCP 隧道监听
func (tg *TunnelGateway) StartTCP(ctx context.Context, tunnel core.Tunnel) error {
	listenAddr := fmt.Sprintf(":%d", tunnel.ListenPort)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("tcp listen %s: %w", listenAddr, err)
	}

	tg.registry.Register(tunnel.Name, listener)
	slog.Info("TCP tunnel listening", "tunnel", tunnel.Name, "port", tunnel.ListenPort)

	go func() {
		defer tg.registry.Unregister(tunnel.Name)

		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					slog.Error("TCP accept error", "tunnel", tunnel.Name, "error", err)
					continue
				}
			}

			go tg.handleTCPConn(ctx, conn, tunnel)
		}
	}()

	return nil
}

func (tg *TunnelGateway) handleTCPConn(ctx context.Context, conn net.Conn, tunnel core.Tunnel) {
	defer conn.Close()

	if err := tg.sem.Acquire(ctx); err != nil {
		return
	}
	defer tg.sem.Release()

	// 使用索引查找目标节点
	node := tg.findNodeForTunnel(ctx, tunnel.Name)
	if node == nil {
		slog.Warn("No node found for TCP tunnel", "tunnel", tunnel.Name)
		return
	}

	stream, err := node.Session.OpenStream()
	if err != nil {
		slog.Error("Failed to open smux stream", "tunnel", tunnel.Name, "nodeId", node.ID, "error", err)
		return
	}
	defer stream.Close()

	slog.Debug("TCP connection forwarded",
		"tunnel", tunnel.Name,
		"srcAddr", conn.RemoteAddr(),
		"nodeId", node.ID,
	)

	biCopy(stream, conn)
}

// findNodeForTunnel 查找拥有指定隧道的在线节点
func (tg *TunnelGateway) findNodeForTunnel(ctx context.Context, tunnelName string) *core.Node {
	// 优先使用索引查找
	if node := tg.findByTunnelName(ctx, tunnelName); node != nil {
		return node
	}

	// 索引未命中，全量扫描兜底
	nodes := tg.nodeMgr.GetAll(ctx)
	for _, n := range nodes {
		if n.Status != core.NodeStatusOnline {
			continue
		}
		for _, t := range n.Tunnels {
			if t.Name == tunnelName {
				return n
			}
		}
	}
	return nil
}
