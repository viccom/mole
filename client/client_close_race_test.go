package moleAgent_client

import (
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestRunReturnsAfterCloseBeforeRun A-1 语义洞的确定性重现（不依赖竞态抖动）：
// Close 先于 Run 执行时 c.cancel 尚未赋值、cancel() 永久丢失，c.closed 虽被
// 关闭但主循环只 watch ctx.Done —— 修复前 Run 永不退出（无限重连），修复后
// 主循环与 c.sleep 均监听 c.closed，立即返回 context.Canceled。
func TestRunReturnsAfterCloseBeforeRun(t *testing.T) {
	oldWriter := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(oldWriter)

	cfg := DefaultConfig()
	cfg.NodeID = "closed01" // 显式 NodeID：短路 node.id 文件读写
	cfg.NodeIDFile = filepath.Join(t.TempDir(), "node.id")
	cfg.ServerAddr = "127.0.0.1:1" // 不可达地址，避免真实连接
	cfg.ReconnectInterval = 10 * time.Millisecond

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	c.Close() // 先于 Run：c.cancel 必为 nil，cancel 丢失路径确定性触发

	done := make(chan error, 1)
	go func() { done <- c.Run(context.Background()) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run 应以 context.Canceled 退出，got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close-先于-Run 后 Run 未退出（cancel 丢失，主循环未 watch c.closed）")
	}
}

// TestCloseConcurrentWithRun 回归 client.Client 的 c.cancel 数据竞争：
// Run（独立 goroutine）在 :195 写 c.cancel，Close（调用方 goroutine）在 :272
// 无同步读同一字段 —— go test -race 必报。
//
// 每次迭代用全新 Client（单次 Run 生命周期设计）+ 不可达 ServerAddr，闭环为：
//  1. go Run(parentCtx)
//  2. 调 Close()（偶数迭代立即调；奇数迭代先 Gosched 让 Run 先跑到赋值行，
//     覆盖两种交错：Close 读到 nil / Close 读到已赋值的 cancel）
//  3. 等 Run 退出。注意：Close 早于 Run 赋值读到 nil 的窗口里 cancel 会丢失
//     （Run 主循环不 watch c.closed，只 watch ctx.Done —— 已知语义洞，本测试
//     不扩大断言范围），因此设 rescue 计时器兜底取消 parent ctx：
//     既保证迭代必然终止（测试不挂死），也保证泄漏的 Run 不跨迭代存活。
//  4. hardFail 计时器兜底：连 parent ctx 取消都无法让 Run 退出时以失败告终
//     而不是挂死。
func TestCloseConcurrentWithRun(t *testing.T) {
	// nil 窗口迭代里 Run 会以 ReconnectInterval 节奏刷重连日志，静默之
	oldWriter := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(oldWriter)

	const (
		iterations    = 100
		rescueAfter   = 200 * time.Millisecond // Run 应在此前因 Close 的 cancel 退出；否则 parent ctx 兜底
		hardFailAfter = 5 * time.Second        // 兜底取消后仍不退出 = 失败（防挂死）
	)

	for i := 0; i < iterations; i++ {
		cfg := DefaultConfig()
		cfg.NodeID = "race0001" // 显式 NodeID：短路 node.id 文件读写
		cfg.NodeIDFile = filepath.Join(t.TempDir(), "node.id")
		cfg.ServerAddr = "127.0.0.1:1" // 不可达地址，避免真实连接
		cfg.ReconnectInterval = 10 * time.Millisecond

		c, err := New(cfg)
		if err != nil {
			t.Fatalf("iteration %d: New failed: %v", i, err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx) }()

		if i%2 == 1 {
			runtime.Gosched() // 抖动：让 Run 先执行到 c.cancel 赋值行
		}
		c.Close()

		rescue := time.NewTimer(rescueAfter)
		hardFail := time.NewTimer(hardFailAfter)
		defer rescue.Stop()
		defer hardFail.Stop()

	waitExit:
		for {
			select {
			case <-done:
				break waitExit
			case <-rescue.C:
				// Close 在 Run 赋值前读到 nil 的窗口（cancel 丢失，已知语义洞）：
				// Run 只认 ctx.Done，用 parent ctx 兜底回收本迭代
				cancel()
			case <-hardFail.C:
				cancel()
				t.Fatalf("iteration %d: Run did not exit within %v even after parent-context fallback",
					i, hardFailAfter)
			}
		}
		cancel()
	}
}
