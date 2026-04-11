package tunnel

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestListenerRegistry(t *testing.T) {
	reg := NewListenerRegistry()

	// 创建测试监听器
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	reg.Register("test", l)

	got, ok := reg.Get("test")
	if !ok {
		t.Fatal("Get returned false")
	}
	if got != l {
		t.Error("Get returned wrong listener")
	}

	names := reg.List()
	if len(names) != 1 || names[0] != "test" {
		t.Errorf("expected [test], got %v", names)
	}

	reg.Unregister("test")
	_, ok = reg.Get("test")
	if ok {
		t.Error("should be unregistered")
	}
}

func TestListenerRegistryReplace(t *testing.T) {
	reg := NewListenerRegistry()

	l1, _ := net.Listen("tcp", "127.0.0.1:0")
	l2, _ := net.Listen("tcp", "127.0.0.1:0")

	reg.Register("test", l1)
	reg.Register("test", l2) // 应该关闭 l1

	got, _ := reg.Get("test")
	if got != l2 {
		t.Error("should be the new listener")
	}

	reg.StopAll()
}

func TestListenerRegistryStopAll(t *testing.T) {
	reg := NewListenerRegistry()

	l1, _ := net.Listen("tcp", "127.0.0.1:0")
	l2, _ := net.Listen("tcp", "127.0.0.1:0")

	reg.Register("a", l1)
	reg.Register("b", l2)

	reg.StopAll()

	if len(reg.List()) != 0 {
		t.Error("should be empty after StopAll")
	}
}

func TestSemaphore(t *testing.T) {
	sem := NewSemaphore(2)
	ctx := context.Background()

	// 应该能获取两个
	if err := sem.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sem.Acquire(ctx); err != nil {
		t.Fatal(err)
	}

	// 第三个应该被阻塞，用 cancel 的 ctx 测试
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err := sem.Acquire(cctx)
	if err == nil {
		t.Error("should timeout on third acquire")
	}

	// 释放一个后应该能获取
	sem.Release()
	if err := sem.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSemaphoreCancel(t *testing.T) {
	sem := NewSemaphore(1)
	sem.Acquire(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	err := sem.Acquire(ctx)
	if err == nil {
		t.Error("should fail with cancelled context")
	}
}
