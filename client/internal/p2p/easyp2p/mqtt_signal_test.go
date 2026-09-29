//go:build p2p

package easyp2p

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mqttTestToken 是 mqtt.Token 的自包含 mock，用于 waitMQTTTokenContext 单测。
type mqttTestToken struct {
	done chan struct{}
	err  error
}

func (t *mqttTestToken) Wait() bool {
	<-t.done
	return true
}

func (t *mqttTestToken) WaitTimeout(timeout time.Duration) bool {
	select {
	case <-t.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (t *mqttTestToken) Done() <-chan struct{} { return t.done }
func (t *mqttTestToken) Error() error          { return t.err }

func TestMQTTSignalSessionCloseHasNoFixedDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := &MQTTSignalSession{
		ctx:    ctx,
		cancel: cancel,
	}

	startedAt := time.Now()
	session.Close()
	if elapsed := time.Since(startedAt); elapsed >= 100*time.Millisecond {
		t.Fatalf("MQTTSignalSession.Close took %s, want less than 100ms", elapsed)
	}
}

func TestWaitMQTTTokenContextReturnsCancellationCause(t *testing.T) {
	cancelCause := errors.New("LAN path won")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cancelCause)
	token := &mqttTestToken{done: make(chan struct{})}

	if err := waitMQTTTokenContext(ctx, token); !errors.Is(err, cancelCause) {
		t.Fatalf("waitMQTTTokenContext error = %v, want %v", err, cancelCause)
	}
}

func TestWaitMQTTTokenContextReturnsTokenError(t *testing.T) {
	tokenErr := errors.New("broker rejected request")
	token := &mqttTestToken{done: make(chan struct{}), err: tokenErr}
	close(token.done)

	if err := waitMQTTTokenContext(context.Background(), token); !errors.Is(err, tokenErr) {
		t.Fatalf("waitMQTTTokenContext error = %v, want %v", err, tokenErr)
	}
}
