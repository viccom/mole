package tunnel

import "net"

// countingConn 包装 net.Conn，在每次 Read/Write 时上报统计
type countingConn struct {
	net.Conn
	onRead  func(int)
	onWrite func(int)
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.onRead != nil {
		c.onRead(n)
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 && c.onWrite != nil {
		c.onWrite(n)
	}
	return n, err
}
