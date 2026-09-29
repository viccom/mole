package vpn

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dropProcess 清理测试进程在全局互斥表中的登记（同名实例互斥用）
func dropProcess(name string) {
	globalManagers.mu.Lock()
	delete(globalManagers.procs, name)
	globalManagers.mu.Unlock()
}

// TestStartFailureRollsBackWithoutDeadlock 守护 startLocked 的启动失败回滚：
// 该路径此前调用 pm.Stop()，而 startLocked 已持 lifecycleMu（sync.Mutex 不可
// 重入）→ 自死锁且锁永不释放，后续 Start/Stop 与 tunnel_push 流水线全部冻结。
func TestStartFailureRollsBackWithoutDeadlock(t *testing.T) {
	const name = "test-rollback-deadlock"
	cfg := Config{Binary: BinaryConfig{Name: "moleagent-binary-that-does-not-exist"}}
	pm, err := NewProcessMgr(name, cfg)
	if err != nil {
		t.Fatalf("NewProcessMgr: %v", err)
	}
	t.Cleanup(func() { dropProcess(name) })

	done := make(chan error, 1)
	go func() { done <- pm.Start(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("binary 不存在时 Start 必须失败")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start 挂死：启动失败回滚路径重复获取了 lifecycleMu")
	}

	// 回滚后锁必须仍然可用（死锁会让第二次调用同样挂起）
	stopped := make(chan struct{})
	go func() { pm.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("启动失败后 lifecycleMu 未释放，后续 Stop 永久阻塞")
	}
}

// TestCrashRestartInterruptedByStopDoesNotFatal 守护 handleExit 的迟到重启分支：
// 该分支曾在显式 Unlock 之后走函数级 defer Unlock，触发
// "fatal error: sync: unlock of unlocked mutex"（进程直接终止，不可 recover）。
// 触发条件：崩溃重启的 sleep 窗口内发生 Stop（远程 stop / 隧道被删除或禁用）。
func TestCrashRestartInterruptedByStopDoesNotFatal(t *testing.T) {
	const name = "test-crash-restart-stop"
	cfg := Config{
		Binary: BinaryConfig{Name: "sh", Path: "/bin/sh"},
		Args:   []string{"-c", "exit 1"},
		// RestartDelay 为秒：Stop 必须落在 sleep 窗口内才能命中该分支
		Lifecycle: LifecycleConfig{RestartOnCrash: true, MaxRestarts: 3, RestartDelay: 2},
	}
	pm, err := NewProcessMgr(name, cfg)
	if err != nil {
		t.Fatalf("NewProcessMgr: %v", err)
	}
	t.Cleanup(func() { dropProcess(name) })

	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // 等进程退出并进入重启 sleep
	pm.Stop()                          // 打断迟到重启（命中 !running 早退分支）
	time.Sleep(3 * time.Second)        // 等 handleExit 从 sleep 中醒来

	if pm.IsRunning() {
		t.Fatal("Stop 之后进程不得被迟到重启复活")
	}
}

// Start 的日志不得泄漏 -k 令牌 / -w 密码。
// 回归背景：process.go 直接打印 args（BuildArgs 产物含敏感项）。
// 此测试在修复前必然失败——日志会原样吐出两个密钥。
//
// 注意：本测试替换全局 log 输出，不可 t.Parallel()，也不得与
// 其它并行的、会写日志的测试共存（process_test.go 无并行用例）。
func TestStartLogsRedactedArgs(t *testing.T) {
	const name = "test-redacted-args-log"
	// 用测试二进制自身作为「存在的可执行文件」：findBinary 走 cfg.Binary.Path
	// 的绝对路径分支即返回，不会真的启动它之前失败——日志点在 findBinary 之后
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cfg := Config{
		Binary: BinaryConfig{Name: filepath.Base(self), Path: self},
		VNT: &VNTConfig{
			Enabled:  true,
			Token:    "LOG-LEAK-TOKEN-9d3f",
			Password: "LOG-LEAK-PASS-a71c",
		},
	}
	pm, err := NewProcessMgr(name, cfg)
	if err != nil {
		t.Fatalf("NewProcessMgr: %v", err)
	}
	t.Cleanup(func() { dropProcess(name) })

	oldW, oldF := log.Writer(), log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(oldW)
		log.SetFlags(oldF)
	})

	// 启动会真实执行测试二进制（无参数、立即退出），不影响断言：
	// 命令行日志在 exec 之前写出
	_ = pm.Start(context.Background())
	pm.Stop()

	out := buf.String()
	if out == "" {
		t.Fatal("Start() produced no log output; the command log line is no longer reached")
	}
	for _, secret := range []string{"LOG-LEAK-TOKEN-9d3f", "LOG-LEAK-PASS-a71c"} {
		if strings.Contains(out, secret) {
			t.Fatalf("vpn Start() log leaked %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "<redacted>") {
		t.Fatalf("expected redaction marker in log output:\n%s", out)
	}
}
