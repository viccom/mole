package ser2mq

import (
	"fmt"
	"io"
	"sync"
	"time"

	"go.bug.st/serial"
)

// SerialConfig 串口配置
type SerialConfig struct {
	Port     string  `json:"port"`
	BaudRate int     `json:"baudrate"`
	DataBits int     `json:"databits"`
	StopBits float64 `json:"stopbits"`
	Parity   string  `json:"parity"` // N/E/O/M/S
	Timeout  int     `json:"timeout"` // 读超时毫秒
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
	case 1.5:
		return serial.OnePointFiveStopBits
	case 2:
		return serial.TwoStopBits
	default:
		return serial.OneStopBit
	}
}

// SerialConn 串口连接接口
type SerialConn interface {
	io.Reader
	io.Writer
	io.Closer
}

// serialConn 串口连接实现
type serialConn struct {
	mu   sync.Mutex
	port serial.Port
	cfg  SerialConfig
}

// OpenSerial 打开串口
func OpenSerial(cfg SerialConfig) (SerialConn, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// 存量 Para 可能缺 timeout 字段（零值）：go.bug.st 默认阻塞读（VMIN=1 无限期），
	// 串口空闲时读循环永久卡死，Stop() 的 wg.Wait() 会在 serial.Close() 之前死锁。
	// 与 ser2net 的 Validate 强制 timeout>0 同理，这里回退到默认值。
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultSerialConfig().Timeout
	}

	mode := cfg.toMode()
	port, err := serial.Open(cfg.Port, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial %s: %w", cfg.Port, err)
	}

	port.SetReadTimeout(time.Duration(cfg.Timeout) * time.Millisecond)

	return &serialConn{port: port, cfg: cfg}, nil
}

// Read 读取数据（copy-and-release 模式，不持有锁）
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

// Write 写入数据（copy-and-release 模式）
func (s *serialConn) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	port := s.port
	s.mu.Unlock()

	if port == nil {
		return 0, fmt.Errorf("serial port closed")
	}

	n, err = port.Write(p)
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
	type timeoutInterface interface{ Timeout() bool }
	if te, ok := err.(timeoutInterface); ok && te.Timeout() {
		return true
	}
	msg := err.Error()
	return msg == "serial: timeout" || msg == "timed out"
}

// SanitizePortName 清理串口名称用于 MQTT 主题
func SanitizePortName(port string) string {
	name := port
	if len(name) > 5 && name[:5] == "/dev/" {
		name = name[5:]
	}
	if len(name) > 4 && name[:4] == `\\.\` {
		name = name[4:]
	}
	return name
}
