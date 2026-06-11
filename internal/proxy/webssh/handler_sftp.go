package webssh

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"

	"github.com/pkg/sftp"
)

// handleFileReadReq 处理文件内容读取请求（用于文本预览，限制 64KB）
func (h *Handler) handleFileReadReq(payload []byte, sw *mutexWriter) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		writeFileAck(sw, false, "invalid request")
		return
	}
	if req.Path == "" {
		writeFileAck(sw, false, "empty path")
		return
	}

	sc, err := h.getSFTPClient()
	if err != nil {
		writeFileAck(sw, false, err.Error())
		return
	}

	f, err := sc.Open(req.Path)
	if err != nil {
		writeFileAck(sw, false, fmt.Sprintf("open file: %v", err))
		return
	}
	defer f.Close()

	const maxSize = 65000 // 预留 ~500B 给 JSON 包装，避免超出 uint16 协议上限
	buf := make([]byte, maxSize)
	n := 0
	for n < maxSize {
		nn, err := f.Read(buf[n:])
		n += nn
		if err != nil {
			if err != io.EOF {
				writeFileAck(sw, false, fmt.Sprintf("read file: %v", err))
				return
			}
			break
		}
	}

	resp, _ := json.Marshal(map[string]any{
		"ok":   true,
		"path": req.Path,
		"data": string(buf[:n]),
		"truncated": n == maxSize,
	})
	sw.mu.Lock()
	defer sw.mu.Unlock()
	writeWebSSHMsg(sw.w, msgFileReadResp, resp)
}

// getSFTPClient 懒创建 SFTP 客户端（复用现有 SSH 连接）
// SSH 重连后旧 sftpClient 会失效，通过 Getwd 探测自动重建
func (h *Handler) getSFTPClient() (*sftp.Client, error) {
	h.sftpMu.Lock()
	defer h.sftpMu.Unlock()
	if h.sftpClient != nil {
		if _, err := h.sftpClient.Getwd(); err == nil {
			return h.sftpClient, nil
		}
		h.sftpClient.Close()
		h.sftpClient = nil
	}
	client, err := h.getSSHClient()
	if err != nil {
		return nil, err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("sftp new client: %w", err)
	}
	h.sftpClient = sc
	return h.sftpClient, nil
}

// fileEntry 文件列表单项
type fileEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	ModTime  string `json:"mod_time"`
	IsDir    bool   `json:"is_dir"`
}

// handleFileListReq 处理目录列表请求
func (h *Handler) handleFileListReq(payload []byte, sw *mutexWriter) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		writeFileAck(sw, false, "invalid request")
		return
	}
	path := req.Path
	if path == "" {
		path = "/"
	}

	sc, err := h.getSFTPClient()
	if err != nil {
		writeFileAck(sw, false, err.Error())
		return
	}

	files, err := sc.ReadDir(path)
	if err != nil {
		writeFileAck(sw, false, fmt.Sprintf("read dir: %v", err))
		return
	}

	home := detectHomeDir(sc)

	entries := make([]fileEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, fileEntry{
			Name:    f.Name(),
			Size:    f.Size(),
			Mode:    f.Mode().String(),
			ModTime: f.ModTime().Format("2006-01-02 15:04:05"),
			IsDir:   f.IsDir(),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	resp, _ := json.Marshal(map[string]any{
		"path":  path,
		"home":  home,
		"files": entries,
	})
	sw.mu.Lock()
	defer sw.mu.Unlock()
	writeWebSSHMsg(sw.w, msgFileListResp, resp)
}

// handleFileUploadReq 处理上传开始请求，返回打开的 SFTP 文件
func (h *Handler) handleFileUploadReq(payload []byte, sw *mutexWriter) *sftp.File {
	var req struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		writeFileAck(sw, false, "invalid request")
		return nil
	}

	sc, err := h.getSFTPClient()
	if err != nil {
		writeFileAck(sw, false, err.Error())
		return nil
	}

	f, err := sc.Create(req.Path)
	if err != nil {
		writeFileAck(sw, false, fmt.Sprintf("create file: %v", err))
		return nil
	}
	slog.Debug("webssh upload started", "name", h.name, "path", req.Path, "size", req.Size)
	return f
}

// handleFileUploadData 处理上传数据块，返回写入字节数和错误
func (h *Handler) handleFileUploadData(payload []byte, f *sftp.File) (int, error) {
	if f == nil || len(payload) == 0 {
		return 0, nil
	}
	return f.Write(payload)
}

// handleFileUploadEnd 处理上传结束
func (h *Handler) handleFileUploadEnd(payload []byte, f *sftp.File, sw *mutexWriter, writeErr error) {
	if f == nil {
		writeFileAck(sw, false, "no upload in progress")
		return
	}
	path := f.Name()
	stat, statErr := f.Stat()
	f.Close()

	if writeErr != nil {
		slog.Warn("webssh upload write error", "name", h.name, "path", path, "error", writeErr)
		writeFileAck(sw, false, fmt.Sprintf("write error: %v", writeErr))
		return
	}

	var fileSize int64
	if statErr == nil {
		fileSize = stat.Size()
	}

	slog.Debug("webssh upload completed", "name", h.name, "path", path, "size", fileSize)
	resp, _ := json.Marshal(map[string]any{"ok": true, "msg": "upload completed", "size": fileSize})
	sw.mu.Lock()
	defer sw.mu.Unlock()
	writeWebSSHMsg(sw.w, msgFileAckResp, resp)
}

// handleFileDownloadReq 处理文件下载请求
func (h *Handler) handleFileDownloadReq(payload []byte, sw *mutexWriter) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		writeFileAck(sw, false, "invalid request")
		return
	}

	sc, err := h.getSFTPClient()
	if err != nil {
		writeFileAck(sw, false, err.Error())
		return
	}

	f, err := sc.Open(req.Path)
	if err != nil {
		writeFileAck(sw, false, fmt.Sprintf("open file: %v", err))
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		writeFileAck(sw, false, fmt.Sprintf("stat file: %v", err))
		return
	}

	slog.Debug("webssh download started", "name", h.name, "path", req.Path, "size", stat.Size())

	buf := make([]byte, 32768)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			sw.mu.Lock()
			writeErr := writeWebSSHMsg(sw.w, msgFileDataResp, buf[:n])
			sw.mu.Unlock()
			if writeErr != nil {
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			writeFileAck(sw, false, fmt.Sprintf("read file: %v", err))
			return
		}
	}

	resp, _ := json.Marshal(map[string]any{
		"ok":   true,
		"msg":  "download completed",
		"size": stat.Size(),
		"name": stat.Name(),
	})
	sw.mu.Lock()
	defer sw.mu.Unlock()
	writeWebSSHMsg(sw.w, msgFileAckResp, resp)
}

// handleFileDeleteReq 处理删除请求
func (h *Handler) handleFileDeleteReq(payload []byte, sw *mutexWriter) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		writeFileAck(sw, false, "invalid request")
		return
	}
	if req.Path == "" || req.Path == "/" {
		writeFileAck(sw, false, "invalid path")
		return
	}

	sc, err := h.getSFTPClient()
	if err != nil {
		writeFileAck(sw, false, err.Error())
		return
	}

	if err := sc.RemoveAll(req.Path); err != nil {
		writeFileAck(sw, false, fmt.Sprintf("delete: %v", err))
		return
	}
	slog.Debug("webssh deleted", "name", h.name, "path", req.Path)
	writeFileAck(sw, true, "deleted")
}

// handleFileMkdirReq 处理创建目录请求
func (h *Handler) handleFileMkdirReq(payload []byte, sw *mutexWriter) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		writeFileAck(sw, false, "invalid request")
		return
	}

	sc, err := h.getSFTPClient()
	if err != nil {
		writeFileAck(sw, false, err.Error())
		return
	}

	if err := sc.MkdirAll(req.Path); err != nil {
		writeFileAck(sw, false, fmt.Sprintf("mkdir: %v", err))
		return
	}
	slog.Debug("webssh mkdir", "name", h.name, "path", req.Path)
	writeFileAck(sw, true, "created")
}

// detectHomeDir 探测 home 目录
func detectHomeDir(sc *sftp.Client) string {
	if wd, err := sc.Getwd(); err == nil && wd != "" {
		return wd
	}
	return "/root"
}
