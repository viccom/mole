package tunnel

import (
	"context"
	"net"

	"golang.org/x/time/rate"
)

// countingConn wraps net.Conn to track bytes and optionally throttle bandwidth.
type countingConn struct {
	net.Conn
	onRead    func(int)
	onWrite   func(int)
	bwLimiter *rate.Limiter // nil = no throttling
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		if c.bwLimiter != nil {
			c.bwLimiter.WaitN(context.Background(), n)
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
			c.bwLimiter.WaitN(context.Background(), n)
		}
		if c.onWrite != nil {
			c.onWrite(n)
		}
	}
	return n, err
}
