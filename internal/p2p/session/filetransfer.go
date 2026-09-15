//go:build p2p

package session

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moleAgent_client/internal/p2p/netutil"
)

// FileMagic is the 4-byte magic number prefixing QUIC file-transfer streams ("FILE").
//
// The receiver uses this to distinguish file transfers from speedtest streams
// (which start with arbitrary non-magic bytes).
const FileMagic uint32 = 0x46494C45 // "FILE"

// FileTransferResult describes a file transfer. It is shared by the send
// return value and the receive (file:in) event.
//
// Path is the absolute local path: the source file path for a send result,
// and the on-disk save path for a receive event (filled by HandleFileRX /
// tcpFileRX after os.Create).
type FileTransferResult struct {
	FileName string
	Path     string
	Size     int64
	Elapsed  time.Duration
	Mbps     float64
}

// absPath returns the absolute form of p, or p unchanged if resolution fails.
// Used only for display/logging, so failure is non-fatal.
func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// MaxFileSize is the per-file receive cap (1 GiB), shared by the QUIC
// (filetransfer.go) and TCP (tcp_file.go) receive paths so a hostile peer
// cannot exhaust memory with an oversized transfer.
const MaxFileSize = 1 << 30

// SendFile sends a file over a new mux stream and returns structured results.
// The caller is responsible for cleaning up the file afterwards.
func SendFile(mux StreamMux, path string) (FileTransferResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileTransferResult{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return FileTransferResult{}, fmt.Errorf("stat %s: %w", path, err)
	}
	fileName := filepath.Base(path)
	fileSize := fi.Size()

	st, err := mux.OpenStream()
	if err != nil {
		return FileTransferResult{}, fmt.Errorf("open stream: %w", err)
	}

	header := make([]byte, 4+2+len(fileName)+8)
	binary.BigEndian.PutUint32(header[0:4], FileMagic)
	binary.BigEndian.PutUint16(header[4:6], uint16(len(fileName)))
	copy(header[6:6+len(fileName)], fileName)
	binary.BigEndian.PutUint64(header[6+len(fileName):], uint64(fileSize))
	if _, err := st.Write(header); err != nil {
		st.Close()
		return FileTransferResult{}, fmt.Errorf("write header: %w", err)
	}

	start := time.Now()
	copyBuf := make([]byte, 1<<20)
	written, err := io.CopyBuffer(st, f, copyBuf)
	st.Close()
	if err != nil {
		return FileTransferResult{}, fmt.Errorf("copy: %w", err)
	}

	elapsed := time.Since(start)
	mbps := float64(written) * 8 / elapsed.Seconds() / 1_000_000
	log.Printf("[SEND] %s sent: %s in %v = %.2f Mbps",
		fileName, netutil.FormatSize(written), elapsed.Round(time.Millisecond), mbps)
	return FileTransferResult{FileName: fileName, Path: absPath(path), Size: written, Elapsed: elapsed, Mbps: mbps}, nil
}

// isWindowsReservedName rejects DOS-era device names that Windows still
// treats specially (COM1, LPT1, CON, NUL, etc). On Windows os.Create("COM1")
// would open the serial device instead of failing — avoid that by refusing
// the transfer regardless of host OS.
func isWindowsReservedName(name string) bool {
	if name == "" {
		return false
	}
	// Strip extension for the check (COM1.txt is also reserved on Windows).
	stem := name
	if i := strings.LastIndexByte(stem, '.'); i > 0 {
		stem = stem[:i]
	}
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// uniqueCreate 原子创建文件：原名冲突（已存在或并发同时创建）则加 " (N)" 序号重试。
// 用 O_EXCL 保证并发安全（H-NEW-3：防同端并发收同名文件时 truncate+交错 io.Copy 致损坏）。
func uniqueCreate(name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		cand := name
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		f, err := os.OpenFile(cand, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, cand, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("too many name collisions for %s", name)
}

// HandleFileRX 接收文件流并落盘。dir 为空 → 保存到进程 CWD（保持原行为）；
// dir 非空 → 保存到 dir。所有安全校验（filepath.Base / 拒绝分隔符 / Windows 保留名 /
// O_EXCL / MaxFileSize / 失败删残）对两种情况一致生效。
// onComplete 在成功保存后调用（可为 nil）；savePath 为落盘绝对路径。
func HandleFileRX(st net.Conn, dir string, onComplete func(fileName, savePath string, size int64, elapsed time.Duration)) {
	header := make([]byte, 2+256+8)
	if _, err := io.ReadFull(st, header[:2]); err != nil {
		log.Printf("[RECV] file header: %v", err)
		return
	}
	nameLen := binary.BigEndian.Uint16(header[:2])
	if nameLen > 255 {
		log.Printf("[RECV] file name too long: %d", nameLen)
		return
	}
	if _, err := io.ReadFull(st, header[2:2+nameLen+8]); err != nil {
		log.Printf("[RECV] file header: %v", err)
		return
	}
	fileName := string(header[2 : 2+nameLen])
	// Sanitize: prevent path traversal (sender uses filepath.Base but receiver
	// must not trust the network). Also reject any path separators.
	fileName = filepath.Base(fileName)
	if strings.ContainsAny(fileName, "/\\") {
		log.Printf("[RECV] suspicious file name rejected: %q", fileName)
		return
	}
	if isWindowsReservedName(fileName) {
		log.Printf("[RECV] rejecting reserved device name: %q", fileName)
		return
	}
	fileSize := binary.BigEndian.Uint64(header[2+nameLen:])
	log.Printf("[RECV] incoming: %s (%s)...", fileName, netutil.FormatSize(int64(fileSize)))

	// Enforce maximum file size (1 GiB); see MaxFileSize.
	if fileSize > MaxFileSize {
		log.Printf("[RECV] file too large: %d bytes (max %d)", fileSize, MaxFileSize)
		return
	}

	// H-NEW-3：uniqueCreate 原子创建 + 序号唯一化，防并发同名写竞态。
	// 并发同名时第二个存为 "name (1).ext"，内容不再交错损坏。
	// dir 为空时 filepath.Join("",fileName)=fileName（相对 → CWD，原行为不变）；
	// 非空时为绝对路径，落指定目录。join 后交 uniqueCreate，序号逻辑对绝对路径同样正确。
	out, saveName, err := uniqueCreate(filepath.Join(dir, fileName))
	if err != nil {
		log.Printf("[RECV] create %s: %v", fileName, err)
		return
	}
	defer out.Close()

	start := time.Now()
	written, copyErr := io.Copy(out, io.LimitReader(st, int64(fileSize)))
	elapsed := time.Since(start)
	// 对端提前关流时 io.Copy 以 EOF 正常结束（written < fileSize）：
	// 截断文件与传输中断同样必须按失败处理，否则半截文件被当作完整接收
	if copyErr != nil || written != int64(fileSize) {
		// 网络/连接中断：删除残缺半成品，不触发 onComplete——失败不得上报为成功。
		_ = os.Remove(saveName)
		log.Printf("[RECV] %s FAILED: copy aborted at %s / %s: %v",
			saveName, netutil.FormatSize(written), netutil.FormatSize(int64(fileSize)), copyErr)
		return
	}
	mbps := float64(written) * 8 / elapsed.Seconds() / 1_000_000
	log.Printf("[RECV] %s saved: %s in %v = %.2f Mbps",
		saveName, netutil.FormatSize(written), elapsed.Round(time.Millisecond), mbps)
	if onComplete != nil {
		// FileName 用 basename（与 SendFile 对称，含 (N) 序号后缀）；dir 非空时 saveName 是
		// 绝对路径，不能直接传给 onComplete——否则前端 FileName 显示完整路径。
		onComplete(filepath.Base(saveName), absPath(saveName), int64(written), elapsed)
	}
}
