package vpn

import (
	"context"
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
