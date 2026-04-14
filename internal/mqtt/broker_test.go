package mqtt

import (
	"context"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
)

func TestBrokerStartNonBlocking(t *testing.T) {
	broker := NewEmbeddedBroker("", "", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- broker.Start(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start should not return error: %v", err)
		}
		// Start returned immediately — non-blocking as expected
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked for more than 2 seconds — should be non-blocking")
	}
}

func TestBrokerStartWithListeners(t *testing.T) {
	broker := NewEmbeddedBroker("127.0.0.1:0", "127.0.0.1:0", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- broker.Start(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked — should be non-blocking")
	}

	// Verify broker can be stopped cleanly
	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestBrokerStopWithContext(t *testing.T) {
	broker := NewEmbeddedBroker("127.0.0.1:0", "", nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	if err := broker.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Give broker time to start serving
	time.Sleep(100 * time.Millisecond)

	// Cancel context should stop the broker
	cancel()
	time.Sleep(200 * time.Millisecond)

	// Verify the server is closed by trying to stop again
	if err := broker.Stop(context.Background()); err != nil {
		t.Fatalf("Stop should be idempotent after cancel, got error: %v", err)
	}
}

func TestBrokerGetClientsAndStats_FilterInlineClient(t *testing.T) {
	broker := NewEmbeddedBroker("", "", nil, nil)

	// 订阅会创建 inline client；该 client 不应暴露给管理接口。
	if err := broker.Subscribe("test/topic", 0, func(string, []byte) {}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	clients := broker.GetClients()
	if len(clients) != 0 {
		t.Fatalf("expected inline client to be filtered, got %+v", clients)
	}

	stats := broker.GetStats()
	if stats.ClientsTotal != 0 || stats.ClientsConnected != 0 {
		t.Fatalf("expected inline client excluded from stats, got %+v", stats)
	}

	// Sanity check: inline client really exists underneath.
	if _, ok := broker.GetServer().Clients.Get(mqtt.InlineClientId); !ok {
		t.Fatal("expected underlying inline client to exist")
	}
}
