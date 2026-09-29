//go:build p2p

package misc

import (
	"os"
	"path/filepath"
	"testing"
)

// 空目录必须合法（= CWD，保持原行为）。
func TestValidateReceiveDir_EmptyIsOK(t *testing.T) {
	if err := ValidateReceiveDir(""); err != nil {
		t.Fatalf("empty dir should be valid (= CWD), got %v", err)
	}
}

// 已存在的目录合法。
func TestValidateReceiveDir_ExistingDir(t *testing.T) {
	d := t.TempDir()
	if err := ValidateReceiveDir(d); err != nil {
		t.Fatalf("existing dir should be valid: %v", err)
	}
}

// 不存在的路径必须拒绝（R10：不让无效 dir 进入 session）。
func TestValidateReceiveDir_NonexistentRejected(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such")
	if err := ValidateReceiveDir(missing); err == nil {
		t.Fatal("nonexistent path should be rejected")
	}
}

// 指向普通文件必须拒绝（不是目录）。
func TestValidateReceiveDir_FileRejected(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := ValidateReceiveDir(f); err == nil {
		t.Fatal("file path should be rejected (not a directory)")
	}
}
