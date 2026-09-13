//go:build p2p

package heartbeat

import (
	"testing"
	"time"
)

func TestTracker_ShouldBeat(t *testing.T) {
	// New tracker: should not beat (just initialised)
	ht := NewTracker()
	if ht.ShouldBeat(time.Second) {
		t.Fatal("new tracker should not beat (recently active)")
	}

	// After write-idle exceeds freq, should beat
	time.Sleep(50 * time.Millisecond)
	if !ht.ShouldBeat(10 * time.Millisecond) {
		t.Fatal("after write-idle > freq, should beat")
	}

	// OnWrite resets the write clock → suppresses heartbeat
	ht.OnWrite()
	if ht.ShouldBeat(time.Second) {
		t.Fatal("after OnWrite, should not beat")
	}

	// OnRead must NOT suppress heartbeat: only write-idleness matters. Reading
	// the peer's heartbeat must not silence our own, or the link goes one-way
	// (peer sends, we suppress, our reader then starves → ReadTimeout).
	ht2 := NewTracker()
	time.Sleep(50 * time.Millisecond)
	ht2.OnRead()
	if !ht2.ShouldBeat(10 * time.Millisecond) {
		t.Fatal("OnRead must not suppress heartbeat (write-idle should still beat)")
	}
}

func TestTracker_IsDead(t *testing.T) {
	ht := NewTracker()

	// Fresh tracker is not dead
	if ht.IsDead(time.Second) {
		t.Fatal("fresh tracker should not be dead")
	}

	// After idle longer than timeout, should be dead
	time.Sleep(50 * time.Millisecond)
	if !ht.IsDead(10 * time.Millisecond) {
		t.Fatal("after idle > timeout, should be dead")
	}

	// OnRead revives
	ht.OnRead()
	if ht.IsDead(time.Second) {
		t.Fatal("after OnRead, should not be dead")
	}
}

func TestTracker_LastActivity(t *testing.T) {
	ht := NewTracker()
	before := time.Now()
	la := ht.LastActivity()
	if la.Before(before) || la.After(time.Now()) {
		t.Fatalf("last activity out of range: %v", la)
	}
}
