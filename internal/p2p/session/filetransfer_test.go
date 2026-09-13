//go:build p2p

package session

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFileStream 构造 HandleFileRX 期望的接收流格式
// [2 字节 nameLen][name][8 字节 size][content]，返回读取端 conn。
// 写入在 goroutine 内进行（net.Pipe 同步无缓冲，必须并发写）。
func writeFileStream(t *testing.T, name string, content []byte) net.Conn {
	t.Helper()
	a, b := net.Pipe()
	go func() {
		defer a.Close()
		hdr := make([]byte, 2+len(name)+8)
		binary.BigEndian.PutUint16(hdr[:2], uint16(len(name)))
		copy(hdr[2:2+len(name)], name)
		binary.BigEndian.PutUint64(hdr[2+len(name):], uint64(len(content)))
		_, _ = a.Write(hdr)
		_, _ = a.Write(content)
	}()
	return b
}

// 空目录必须保持原行为：保存到进程 CWD（R9 回归保障，"空值即 CWD"）。
func TestHandleFileRX_EmptyDirSavesToCWD(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	name := fmt.Sprintf("tdd-empty-cwd-%d.txt", time.Now().UnixNano())
	st := writeFileStream(t, name, []byte("payload-cwd"))
	st.SetDeadline(time.Now().Add(5 * time.Second))

	var got string
	HandleFileRX(st, "", func(_, savePath string, _ int64, _ time.Duration) { got = savePath })
	st.Close()

	want := filepath.Join(cwd, name)
	if got != want {
		t.Fatalf("savePath=%q want %q (CWD=%s)", got, want, cwd)
	}
	b, err := os.ReadFile(want)
	if err != nil || string(b) != "payload-cwd" {
		t.Fatalf("CWD file missing/wrong: %v %q", err, string(b))
	}
	_ = os.Remove(want)
}

// 非空目录：文件保存到指定 dir（需求 4 核心行为）。
func TestHandleFileRX_NonEmptyDirSavesToDir(t *testing.T) {
	dir := t.TempDir()
	name := "tdd-in-dir.txt"
	st := writeFileStream(t, name, []byte("payload-dir"))
	st.SetDeadline(time.Now().Add(5 * time.Second))

	var got string
	HandleFileRX(st, dir, func(_, savePath string, _ int64, _ time.Duration) { got = savePath })
	st.Close()

	want := filepath.Join(dir, name)
	if got != want {
		t.Fatalf("savePath=%q want %q", got, want)
	}
	b, err := os.ReadFile(want)
	if err != nil || string(b) != "payload-dir" {
		t.Fatalf("dir file missing/wrong: %v %q", err, string(b))
	}
}

// 路径安全必须保留：对端发 "../../../etc/x" 时 filepath.Base sanitize，
// 保存到 dir 内、不逃逸（需求 4 不得破坏现有安全校验）。
func TestHandleFileRX_DirRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	st := writeFileStream(t, "../../../etc/passwd-tdd", []byte("evil"))
	st.SetDeadline(time.Now().Add(5 * time.Second))

	var got string
	HandleFileRX(st, dir, func(_, savePath string, _ int64, _ time.Duration) { got = savePath })
	st.Close()

	// Base("../../../etc/passwd-tdd") = "passwd-tdd"，join 到 dir 内，不逃逸。
	if filepath.Dir(got) != dir {
		t.Fatalf("path escaped dir: got=%q dir=%q", got, dir)
	}
	_ = os.Remove(got)
}
