package tunnel

import (
	"context"
	"net"

	"golang.org/x/time/rate"
)

// countingConn wraps net.Conn to track bytes and optionally throttle bandwidth.
type countingConn struct {
	net.Conn
	ctx       context.Context
	onRead    func(int)
	onWrite   func(int)
	bwLimiter *rate.Limiter // nil = no throttling
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		if c.bwLimiter != nil {
			if waitErr := waitBurst(c.ctx, c.bwLimiter, n); waitErr != nil {
				return n, waitErr
			}
		}
		if c.onRead != nil {
			c.onRead(n)
		}
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		if c.bwLimiter != nil {
			if waitErr := waitBurst(c.ctx, c.bwLimiter, n); waitErr != nil {
				return n, waitErr
			}
		}
		if c.onWrite != nil {
			c.onWrite(n)
		}
	}
	return n, err
}

// waitBurst waits for n tokens from the limiter, splitting into burst-sized
// chunks to handle cases where n exceeds the limiter's burst capacity.
// Returns an error if the context is cancelled while waiting.
func waitBurst(ctx context.Context, l *rate.Limiter, n int) error {
	burst := l.Burst()
	for n > 0 {
		chunk := n
		if chunk > burst {
			chunk = burst
		}
		if err := l.WaitN(ctx, chunk); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}
