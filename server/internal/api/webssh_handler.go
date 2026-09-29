package api

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"moleAgent_Serv/internal/auth"
	"moleAgent_Serv/internal/core"
	"moleAgent_Serv/internal/node"
)

// 消息类型（与客户端 webssh handler 一致）
const (
	websshMsgData   byte = 0x01 // 终端数据
	websshMsgResize byte = 0x02 // 窗口大小调整
	websshMsgKeep   byte = 0x03 // 心跳

	// 文件操作请求（浏览器 → 客户端）
	websshMsgFileListReq     byte = 0x04
	websshMsgFileUploadReq   byte = 0x05
	websshMsgFileUploadData  byte = 0x06
	websshMsgFileUploadEnd   byte = 0x07
	websshMsgFileDownloadReq byte = 0x08
	websshMsgFileDeleteReq   byte = 0x09
	websshMsgFileMkdirReq    byte = 0x0A
	websshMsgFileReadReq     byte = 0x0B // 文件内容读取请求

	// 文件操作响应（客户端 → 浏览器）
	websshMsgFileListResp byte = 0x0C
	websshMsgFileDataResp byte = 0x0D
	websshMsgFileAckResp  byte = 0x0E
	websshMsgFileReadResp byte = 0x0F // 文件内容读取响应
)

var websshUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // 非浏览器客户端（如 CLI）无 Origin 头
		}
		// 同源检查：Origin 的 host 必须与请求 host 一致
		return origin == "http://"+r.Host || origin == "https://"+r.Host
	},
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

// WebSSHHandler 处理 WebSSH WebSocket 连接
type WebSSHHandler struct {
	nodeMgr *node.ShardedNodeManager
}

func NewWebSSHHandler(nodeMgr *node.ShardedNodeManager) *WebSSHHandler {
	return &WebSSHHandler{nodeMgr: nodeMgr}
}

// Handle 处理 WebSocket 升级并桥接到 smux 流
// 路由: GET /api/v1/tunnels/:name/webssh
func (h *WebSSHHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// 提取隧道名
	tunnelName := strings.TrimPrefix(r.URL.Path, "/api/v1/tunnels/")
	tunnelName = strings.TrimSuffix(tunnelName, "/webssh")
	tunnelName = strings.TrimSuffix(tunnelName, "/")
	if tunnelName == "" {
		ResponseError(w, http.StatusBadRequest, 400, "tunnel name required")
		return
	}

	// JWT 认证
	claims := auth.GetClaims(r.Context())
	if claims == nil {
		ResponseError(w, http.StatusUnauthorized, 401, "authentication required")
		return
	}

	// 查找节点和隧道
	var targetNode *core.Node
	var found bool
	nodes := h.nodeMgr.GetAll(r.Context())
	isAdmin := IsAdmin(claims)

	for _, n := range nodes {
		if n.Status != core.NodeStatusOnline {
			continue
		}
		if !isAdmin && n.OwnerUserID != claims.UserID {
			continue
		}
		for _, t := range n.Tunnels {
			if t.Name == tunnelName && t.IsEnabled() && t.Type == core.TunnelTypeWebSSH {
				targetNode = n
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found || targetNode == nil {
		ResponseError(w, http.StatusNotFound, 404, "webssh tunnel not found: "+tunnelName)
		return
	}

	// 获取 smux session
	session, err := h.nodeMgr.GetSession(r.Context(), targetNode.ID)
	if err != nil {
		slog.Error("WebSSH: failed to get session", "tunnel", tunnelName, "nodeId", targetNode.ID, "error", err)
		ResponseError(w, http.StatusBadGateway, 502, "node session unavailable")
		return
	}

		// 打开 smux 流
		stream, err := session.OpenStream()
		if err != nil {
			slog.Error("WebSSH: failed to open stream", "tunnel", tunnelName, "nodeId", targetNode.ID, "error", err)
			ResponseError(w, http.StatusBadGateway, 502, "failed to open stream to client")
			return
		}
		defer stream.Close()

		// 发送流标识头，让客户端 dispatchStream 识别为 webssh 流
		if _, err := stream.Write(append([]byte{0x01}, tunnelName...)); err != nil {
			slog.Error("WebSSH: failed to write stream header", "tunnel", tunnelName, "error", err)
			return
		}
		if _, err := stream.Write([]byte{'\n'}); err != nil {
			slog.Error("WebSSH: failed to write stream header newline", "tunnel", tunnelName, "error", err)
			return
		}


	// 升级为 WebSocket
	ws, err := websshUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSSH: websocket upgrade failed", "tunnel", tunnelName, "error", err)
		return
	}
	defer ws.Close()

	slog.Info("WebSSH session started", "tunnel", tunnelName, "nodeId", targetNode.ID, "user", claims.UserID)

	// 空闲超时：每次收到消息时刷新，而非硬截断
	const idleTimeout = 4 * time.Hour
	ws.SetReadDeadline(time.Now().Add(idleTimeout))

	// 双向桥接
	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	// 保护 smux stream 并发写入（wsToSmux + 心跳）
	var streamMu sync.Mutex
	lockedStream := &lockWriter{mu: &streamMu, w: stream}

	// WS → smux（解析消息类型后转发）
	go func() {
		defer closeDone()
		h.wsToSmux(ws, lockedStream, tunnelName)
	}()

	// smux → WS（SSH 输出直接透传）
	go func() {
		defer closeDone()
		h.smuxToWs(ws, stream, tunnelName)
	}()

	// 心跳 goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				streamMu.Lock()
				err := writeWebSSHMsg(stream, websshMsgKeep, nil)
				streamMu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	<-done
	slog.Info("WebSSH session ended", "tunnel", tunnelName, "nodeId", targetNode.ID)
}

// wsToSmux 从 WebSocket 读取消息，解析类型后写入 smux 流
func (h *WebSSHHandler) wsToSmux(ws *websocket.Conn, stream io.Writer, tunnelName string) {
	ws.SetReadLimit(64 * 1024) // 文件上传数据块可达 32KB+
	const idleTimeout = 4 * time.Hour
	for {
		ws.SetReadDeadline(time.Now().Add(idleTimeout))
		msgType, data, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Debug("WebSSH: ws read error", "tunnel", tunnelName, "error", err)
			}
			return
		}

		if msgType != websocket.BinaryMessage || len(data) == 0 {
			continue
		}

		msgFlag := data[0]
		payload := data[1:]

		switch msgFlag {
		case websshMsgData:
			if err := writeWebSSHMsg(stream, websshMsgData, payload); err != nil {
				slog.Debug("WebSSH: smux write data error", "tunnel", tunnelName, "error", err)
				return
			}
		case websshMsgResize:
			if err := writeWebSSHMsg(stream, websshMsgResize, payload); err != nil {
				slog.Debug("WebSSH: smux write resize error", "tunnel", tunnelName, "error", err)
				return
			}
		case websshMsgKeep:
			// 客户端心跳，忽略
		case websshMsgFileListReq, websshMsgFileUploadReq, websshMsgFileUploadData,
			websshMsgFileUploadEnd, websshMsgFileDownloadReq, websshMsgFileDeleteReq,
			websshMsgFileMkdirReq, websshMsgFileReadReq:
			// 文件操作消息透传到客户端
			if err := writeWebSSHMsg(stream, msgFlag, payload); err != nil {
				slog.Debug("WebSSH: smux write file msg error", "tunnel", tunnelName, "error", err)
				return
			}
		}
	}
}

// smuxToWs 从 smux 流读取结构化消息，解析后转发到 WebSocket
// 消息格式: [type 1B][len 2B BE][payload]，转发到 WS 时只发 [type 1B][payload]
func (h *WebSSHHandler) smuxToWs(ws *websocket.Conn, stream io.Reader, tunnelName string) {
	for {
		msgType, payload, err := readWebSSHMsgPayload(stream)
		if err != nil {
			if err != io.EOF {
				slog.Debug("WebSSH: smux read error", "tunnel", tunnelName, "error", err)
			}
			return
		}
		ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		// 构建 WS 消息: [type 1B][payload]
		msg := make([]byte, 1+len(payload))
		msg[0] = msgType
		copy(msg[1:], payload)
		if writeErr := ws.WriteMessage(websocket.BinaryMessage, msg); writeErr != nil {
			slog.Debug("WebSSH: ws write error", "tunnel", tunnelName, "error", writeErr)
			return
		}
	}
}

// readWebSSHMsgPayload 从流读取 [type 1B][len 2B BE][payload]，返回 type 和 payload
func readWebSSHMsgPayload(r io.Reader) (byte, []byte, error) {
	var hdr [3]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint16(hdr[1:3])
	if length == 0 {
		return hdr[0], nil, nil
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

// writeWebSSHMsg 写入一条 WebSSH 协议消息到 smux 流
// 格式: [类型 1字节][长度 2字节大端序][载荷]
func writeWebSSHMsg(w io.Writer, msgType byte, payload []byte) error {
	if len(payload) > 65535 {
		return fmt.Errorf("payload too large: %d bytes (max 65535)", len(payload))
	}
	// 合并 header+payload 一次 Write：lockWriter.Write 单次调用即持锁覆盖整条帧，
	// 避免心跳 goroutine 在 header 与 payload 两次 Write 之间插入 keepalive 帧撕裂协议。
	buf := make([]byte, 3+len(payload))
	buf[0] = msgType
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(payload)))
	copy(buf[3:], payload)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write webssh msg: %w", err)
	}
	return nil
}

// lockWriter 保护并发写入 io.Writer
type lockWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (lw *lockWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}
