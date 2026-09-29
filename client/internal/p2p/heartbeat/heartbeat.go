//go:build p2p

// Package heartbeat provides a read/write activity tracker for suppressing
// keep-alive heartbeats when data is actively flowing.
package heartbeat

import (
	"sync"
	"time"
)

// Tracker records the last read and write timestamps for a connection.
// It is safe for concurrent use by one reader goroutine and one writer goroutine.
type Tracker struct {
	lastRead  time.Time
	lastWrite time.Time
	mu        sync.Mutex
}

// NewTracker creates a Tracker initialised to the current time.
func NewTracker() *Tracker {
	now := time.Now()
	return &Tracker{lastRead: now, lastWrite: now}
}

// OnRead records a read event.
func (t *Tracker) OnRead() {
	t.mu.Lock()
	t.lastRead = time.Now()
	t.mu.Unlock()
}

// OnWrite records a write event.
func (t *Tracker) OnWrite() {
	t.mu.Lock()
	t.lastWrite = time.Now()
	t.mu.Unlock()
}

// ShouldBeat returns true when the connection has been write-idle for longer
// than freq, i.e. we haven't sent anything (data or heartbeat) for a while.
//
// 只看写活动、不看 lastRead：早先版本要求"读+写都空闲"才发心跳，但 reader 每收到
// 一帧（含对端的心跳 HB）就调 OnRead，使 lastRead 一直很新、ShouldBeat 恒 false，
// 本端永远不发心跳 → 心跳变单向（一端发、一端抑制），不发的那端 reader 收不到任何
// 数据、35s 后 ReadTimeout 误判断开。数据流时 lastWrite 频繁更新（写数据），自然
// 抑制心跳，无需 lastRead 参与。
func (t *Tracker) ShouldBeat(freq time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	return now.Sub(t.lastWrite) >= freq
}

// IsDead returns true when both read and write have been silent for longer
// than timeout, indicating a likely broken connection.
func (t *Tracker) IsDead(timeout time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	return now.Sub(t.lastRead) > timeout && now.Sub(t.lastWrite) > timeout
}

// LastActivity returns the most recent of lastRead or lastWrite.
func (t *Tracker) LastActivity() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastRead.After(t.lastWrite) {
		return t.lastRead
	}
	return t.lastWrite
}
