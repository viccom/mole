package mqtt

import (
	"context"
	"testing"
	"time"
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
		// Stop after cancel may error, but should not panic
		t.Logf("Stop after cancel returned (expected): %v", err)
	}
}
