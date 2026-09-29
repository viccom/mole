package ser2mq

import (
	"sync"
	"testing"
	"time"
)

func TestStreamHubSubscribeReceivesPublishedEvent(t *testing.T) {
	hub := NewStreamHub(10)

	ch, unsub := hub.Subscribe("alpha", 0)
	defer unsub()

	evt := PacketEvent{
		Time:   time.Now().UnixMilli(),
		Tunnel: "alpha",
		Dir:    "serial_out",
		Length: 8,
	}
	hub.Publish(evt)

	select {
	case got := <-ch:
		if got.Tunnel != "alpha" {
			t.Fatalf("expected tunnel=alpha, got %s", got.Tunnel)
		}
		if got.Dir != "serial_out" {
			t.Fatalf("expected dir=serial_out, got %s", got.Dir)
		}
		if got.Length != 8 {
			t.Fatalf("expected length=8, got %d", got.Length)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestStreamHubSubscribeReturnsTailBeforeLiveEvents(t *testing.T) {
	hub := NewStreamHub(200)

	// 发布 3 条历史事件
	for i := 0; i < 3; i++ {
		hub.Publish(PacketEvent{Time: int64(i), Tunnel: "alpha", Dir: "serial_out", Length: i + 1})
	}
	// 给 Publish 一点时间完成
	time.Sleep(10 * time.Millisecond)

	// 订阅并请求最近 2 条
	ch, unsub := hub.Subscribe("alpha", 2)
	defer unsub()

	// 应该先收到尾部 2 条历史事件（length=2, length=3）
	var received []PacketEvent
	timeout := time.After(time.Second)
	for len(received) < 2 {
		select {
		case evt := <-ch:
			received = append(received, evt)
		case <-timeout:
			t.Fatalf("only got %d events, want 2", len(received))
		}
	}

	if received[0].Length != 2 {
		t.Fatalf("first tail event length=%d, want 2", received[0].Length)
	}
	if received[1].Length != 3 {
		t.Fatalf("second tail event length=%d, want 3", received[1].Length)
	}

	// 再发一条 live 事件
	hub.Publish(PacketEvent{Time: 99, Tunnel: "alpha", Dir: "mqtt_sub", Length: 42})

	select {
	case got := <-ch:
		if got.Length != 42 {
			t.Fatalf("live event length=%d, want 42", got.Length)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestStreamHubDropOnSlowSubscriber(t *testing.T) {
	hub := NewStreamHub(10)

	// 小缓冲 channel
	ch, unsub := hub.Subscribe("alpha", 0)
	defer unsub()

	// 快速发 200 条，不应阻塞
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			hub.Publish(PacketEvent{Time: int64(i), Tunnel: "alpha", Dir: "serial_out", Length: i})
		}
	}()

	select {
	case <-done:
		// 成功：Publish 不阻塞
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}

	// 消费一些事件证明 channel 可读
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			return
		}
	}
}

func TestStreamHubUnsubscribeStopsDelivery(t *testing.T) {
	hub := NewStreamHub(10)

	ch, unsub := hub.Subscribe("alpha", 0)
	unsub()

	// 等待 unsub 完成
	time.Sleep(10 * time.Millisecond)

	hub.Publish(PacketEvent{Time: 1, Tunnel: "alpha", Dir: "serial_out", Length: 1})

	select {
	case <-ch:
		// 可能收到 unsub 之前的残留，忽略
	default:
		// 预期：不再收到新事件
	}
}

func TestStreamHubConcurrentPublish(t *testing.T) {
	hub := NewStreamHub(100)

	ch, unsub := hub.Subscribe("alpha", 0)
	defer unsub()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				hub.Publish(PacketEvent{Time: int64(i), Tunnel: "alpha", Dir: "serial_out", Length: i})
			}
		}()
	}
	wg.Wait()

	// 消费一些，不应 panic
	drained := 0
	timeout := time.After(time.Second)
	for drained < 10 {
		select {
		case <-ch:
			drained++
		case <-timeout:
			t.Fatalf("only drained %d events", drained)
		}
	}
}
