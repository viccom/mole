package node

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"moleAgent_Serv/internal/core"

	"github.com/xtaci/smux"
)

// newSmuxSessionPair creates a real smux server+client session pair over net.Pipe.
// Returns the server session (for use in tests) and a cleanup function.
func newSmuxSessionPair() (serverSession *smux.Session, cleanup func()) {
	conn1, conn2 := net.Pipe()

	// Server side
	serverSession, _ = smux.Server(conn1, nil)

	// Client side (needed to keep server session alive)
	clientSession, _ := smux.Client(conn2, nil)

	cleanup = func() {
		clientSession.Close()
		serverSession.Close()
		conn1.Close()
		conn2.Close()
	}

	return serverSession, cleanup
}

func TestGetSession_Success(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	session, cleanup := newSmuxSessionPair()
	defer cleanup()

	node := &core.Node{
		ID:     "node-online",
		Name:   "Online Node",
		Status: core.NodeStatusOnline,
	}

	if err := mgr.Add(ctx, node); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	mgr.AddSession(ctx, node.ID, session)

	got, err := mgr.GetSession(ctx, "node-online")
	if err != nil {
		t.Fatalf("GetSession returned unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("GetSession returned nil session")
	}
	if got != session {
		t.Error("GetSession returned wrong session pointer")
	}
}

func TestGetSession_NodeNotFound(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	_, err := mgr.GetSession(ctx, "nonexistent")
	if !errors.Is(err, core.ErrNodeNotFound) {
		t.Errorf("expected ErrNodeNotFound, got: %v", err)
	}
}

func TestGetSession_NodeOffline(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	node := &core.Node{
		ID:     "node-offline",
		Name:   "Offline Node",
		Status: core.NodeStatusOffline,
	}

	if err := mgr.Add(ctx, node); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	_, err := mgr.GetSession(ctx, "node-offline")
	if !errors.Is(err, core.ErrNodeOffline) {
		t.Errorf("expected ErrNodeOffline, got: %v", err)
	}
}

func TestGetSession_NilSession(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	node := &core.Node{
		ID:     "node-nil-session",
		Name:   "Nil Session Node",
		Status: core.NodeStatusOnline,
	}

	if err := mgr.Add(ctx, node); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	_, err := mgr.GetSession(ctx, "node-nil-session")
	if !errors.Is(err, core.ErrNodeOffline) {
		t.Errorf("expected ErrNodeOffline for nil session, got: %v", err)
	}
}

func TestGetSession_ConcurrentSafety(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	session, cleanup := newSmuxSessionPair()
	defer cleanup()

	node := &core.Node{
		ID:     "node-concurrent",
		Name:   "Concurrent Node",
		Status: core.NodeStatusOnline,
	}

	if err := mgr.Add(ctx, node); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	mgr.AddSession(ctx, node.ID, session)

	var (
		wg               sync.WaitGroup
		successCount     atomic.Int64
		notFoundCount    atomic.Int64
		offlineCount     atomic.Int64
		readerGoroutines = 50
	)

	// Barrier: readers start together
	start := make(chan struct{})

	// Launch reader goroutines that repeatedly call GetSession
	for i := 0; i < readerGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				sess, err := mgr.GetSession(ctx, "node-concurrent")
				if err == nil && sess != nil {
					successCount.Add(1)
				} else if errors.Is(err, core.ErrNodeNotFound) {
					notFoundCount.Add(1)
				} else if errors.Is(err, core.ErrNodeOffline) {
					offlineCount.Add(1)
				}
			}
		}()
	}

	// Disconnector goroutine: removes the node after a short delay
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(5 * time.Millisecond)
		mgr.Disconnect(ctx, "node-concurrent")
	}()

	close(start)
	wg.Wait()

	total := successCount.Load() + notFoundCount.Load() + offlineCount.Load()
	if total == 0 {
		t.Error("expected some GetSession results, got 0")
	}

	t.Logf("GetSession concurrent results: success=%d, notFound=%d, offline=%d",
		successCount.Load(), notFoundCount.Load(), offlineCount.Load())

	// After disconnect, all subsequent GetSession calls must return an error
	_, err := mgr.GetSession(ctx, "node-concurrent")
	if !errors.Is(err, core.ErrNodeNotFound) {
		t.Errorf("expected ErrNodeNotFound after disconnect, got: %v", err)
	}
}

func TestUpdate_ConcurrentSafety(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	node := &core.Node{
		ID:     "node-update",
		Name:   "Update Node",
		Status: core.NodeStatusOnline,
	}

	if err := mgr.Add(ctx, node); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	var (
		wg           sync.WaitGroup
		successCount atomic.Int64
		failCount    atomic.Int64
		updaters     = 100
	)

	// Each goroutine increments a counter stored in the node's RemoteAddr
	start := make(chan struct{})

	for i := 0; i < updaters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			err := mgr.Update(ctx, "node-update", func(n *core.Node) {
				// Simulate work: increment the RemoteAddr counter
				n.RemoteAddr = n.RemoteAddr + "."
			})
			if err == nil {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if failCount.Load() > 0 {
		t.Errorf("some Update calls failed: %d", failCount.Load())
	}

	if successCount.Load() != int64(updaters) {
		t.Errorf("expected %d successful updates, got %d", updaters, successCount.Load())
	}

	// Verify all updates were applied atomically (no lost updates)
	updated, ok := mgr.Get(ctx, "node-update")
	if !ok {
		t.Fatal("node should still exist")
	}
	// Each of the 100 updaters appended one "." to RemoteAddr
	expectedLen := updaters // 100 dots
	if len(updated.RemoteAddr) != expectedLen {
		t.Errorf("expected RemoteAddr length %d, got %d — concurrent updates were lost",
			expectedLen, len(updated.RemoteAddr))
	}
}

func TestUpdate_NodeNotFound(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	err := mgr.Update(ctx, "nonexistent", func(n *core.Node) {
		t.Error("callback should not be called for nonexistent node")
	})
	if !errors.Is(err, core.ErrNodeNotFound) {
		t.Errorf("expected ErrNodeNotFound, got: %v", err)
	}
}

func TestRemove_CleansSessionMap(t *testing.T) {
	mgr := NewShardedNodeManager(4)
	ctx := context.Background()

	session, cleanup := newSmuxSessionPair()
	defer cleanup()

	node := &core.Node{
		ID:     "node-remove",
		Name:   "Remove Node",
		Status: core.NodeStatusOnline,
	}
	if err := mgr.Add(ctx, node); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	mgr.AddSession(ctx, node.ID, session)

	if err := mgr.Remove(ctx, node.ID); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	if _, err := mgr.GetSession(ctx, node.ID); !errors.Is(err, core.ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound after remove, got %v", err)
	}
}
