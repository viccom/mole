package ser2mq

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// SerialConfig 串口配置
type SerialConfig struct {
	Port     string
	BaudRate int
	DataBits int
	StopBits float64
	Parity   string // N/E/O
	Timeout  int    // 读超时毫秒
}

// DefaultSerialConfig 返回默认串口配置
func DefaultSerialConfig() SerialConfig {
	return SerialConfig{
		BaudRate: 9600,
		DataBits: 8,
		StopBits: 1.0,
		Parity:   "N",
		Timeout:  3000,
	}
}

// Validate 校验配置
func (s *SerialConfig) Validate() error {
	if s.Port == "" {
		return fmt.Errorf("serial port is required")
	}
	if s.BaudRate <= 0 {
		return fmt.Errorf("invalid baudrate: %d", s.BaudRate)
	}
	switch s.Parity {
	case "N", "E", "O", "M", "S":
	default:
		return fmt.Errorf("invalid parity: %s (expected N/E/O/M/S)", s.Parity)
	}
	return nil
}

// toMode 转换为 serial.Mode
func (s *SerialConfig) toMode() *serial.Mode {
	return &serial.Mode{
		BaudRate: s.BaudRate,
		DataBits: s.DataBits,
		StopBits: s.toStopBits(),
		Parity:   s.toParity(),
	}
}

// toParity 转换为 serial.Parity
func (s *SerialConfig) toParity() serial.Parity {
	switch s.Parity {
	case "N":
		return serial.NoParity
	case "E":
		return serial.EvenParity
	case "O":
		return serial.OddParity
	case "M":
		return serial.MarkParity
	case "S":
		return serial.SpaceParity
	default:
		return serial.NoParity
	}
}

// toStopBits 转换为 serial.StopBits
func (s *SerialConfig) toStopBits() serial.StopBits {
	switch s.StopBits {
	case 1.0:
		return serial.OneStopBit
	case 1.5:
		// iota 1
		return serial.StopBits(1)
	case 2:
		return serial.TwoStopBits
	default:
		return serial.OneStopBit
	}
}

// SerialConn 串口连接接口（带读写锁）
type SerialConn interface {
	ioReader
	ioWriter
	ioCloser
}

// ioReader 串口读取接口
type ioReader interface {
	Read(p []byte) (n int, err error)
}

// ioWriter 串口写入接口
type ioWriter interface {
	Write(p []byte) (n int, err error)
}

// ioCloser 关闭接口
type ioCloser interface {
	Close() error
}

// serialConn 串口连接实现
type serialConn struct {
	mu      sync.RWMutex
	port    serial.Port
	cfg     SerialConfig
}

// OpenSerial 打开串口
func OpenSerial(cfg SerialConfig) (SerialConn, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	mode := cfg.toMode()
	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial %s: %w", cfg.Port, err)
	}

	// 设置读写超时
	readTimeout := time.Duration(cfg.Timeout) * time.Millisecond
	if err := port.SetReadTimeout(readTimeout); err != nil {
		port.Close()
		return nil, fmt.Errorf("set read timeout: %w", err)
	}

	return &serialConn{port: port, cfg: cfg}, nil
}

// Read 读取数据（带读写锁，超时 3 秒无数据不处理）
func (s *serialConn) Read(p []byte) (n int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 检查端口是否有效
	if s.port == nil {
		return 0, fmt.Errorf("serial port closed")
	}

	n, err = s.port.Read(p)
	if err != nil {
		// 超时视为正常情况，无数据
		if isTimeout(err) {
			return 0, nil
		}
		return n, err
	}
	return n, nil
}

// Write 写入数据（带读写锁）
func (s *serialConn) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.port == nil {
		return 0, fmt.Errorf("serial port closed")
	}

	n, err = s.port.Write(p)
	if err != nil {
		return n, fmt.Errorf("write serial: %w", err)
	}
	return n, nil
}

// Close 关闭串口
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

// isTimeout 检查是否是超时错误
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "i/o timeout")
}

// SanitizePortName 清理串口名称用于 MQTT 主题
func SanitizePortName(port string) string {
	// 移除路径前缀
	name := strings.TrimPrefix(port, "/dev/")
	return name
}
