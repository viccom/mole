package ser2net

import (
	"fmt"
	"io"
	"sync"
	"time"

	"go.bug.st/serial"
)

type SerialConn interface {
	io.Reader
	io.Writer
	io.Closer
}

type serialConn struct {
	mu   sync.Mutex
	port serial.Port
}

func OpenSerial(cfg SerialConfig) (SerialConn, error) {
	mode := cfg.toMode()
	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial %s: %w", cfg.Port, err)
	}
	if cfg.Timeout > 0 {
		port.SetReadTimeout(time.Duration(cfg.Timeout) * time.Millisecond)
	}
	return &serialConn{port: port}, nil
}

func (s *serialConn) Read(p []byte) (n int, err error) {
	s.mu.Lock()
	port := s.port
	s.mu.Unlock()
	if port == nil {
		return 0, fmt.Errorf("serial port closed")
	}
	n, err = port.Read(p)
	if err != nil {
		if isTimeout(err) {
			return 0, nil
		}
		return n, err
	}
	return n, nil
}

func (s *serialConn) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	port := s.port
	s.mu.Unlock()
	if port == nil {
		return 0, fmt.Errorf("serial port closed")
	}
	return port.Write(p)
}

func (s *serialConn) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.port == nil {
		return nil
	}
	err := s.port.Close()
	s.port = nil
	return err
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	type timeoutInterface interface{ Timeout() bool }
	if te, ok := err.(timeoutInterface); ok && te.Timeout() {
		return true
	}
	msg := err.Error()
	return msg == "serial: timeout" || msg == "timed out"
}
