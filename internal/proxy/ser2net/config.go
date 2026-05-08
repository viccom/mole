package ser2net

import (
	"fmt"

	"go.bug.st/serial"
)

type SerialConfig struct {
	Port     string  `json:"port"`
	BaudRate int     `json:"baudrate"`
	DataBits int     `json:"databits"`
	StopBits float64 `json:"stopbits"`
	Parity   string  `json:"parity"`
	Timeout  int     `json:"timeout"`
}

type Ser2NetConfig struct {
	Enable  bool         `json:"enable"`
	Serial  SerialConfig `json:"serial"`
	Mode    string       `json:"mode"`
	Address string       `json:"address"`
	MaxConn int          `json:"max_conn"`
}

func (c *Ser2NetConfig) Validate() error {
	if c.Serial.Port == "" {
		return fmt.Errorf("serial port is required")
	}
	if c.Serial.BaudRate <= 0 {
		return fmt.Errorf("invalid baudrate: %d", c.Serial.BaudRate)
	}
	switch c.Serial.DataBits {
	case 5, 6, 7, 8:
	default:
		return fmt.Errorf("invalid databits: %d", c.Serial.DataBits)
	}
	switch c.Serial.StopBits {
	case 1, 1.5, 2:
	default:
		return fmt.Errorf("invalid stopbits: %v", c.Serial.StopBits)
	}
	switch c.Serial.Parity {
	case "", "N", "E", "O", "M", "S":
	default:
		return fmt.Errorf("invalid parity: %q", c.Serial.Parity)
	}
	if c.Serial.Timeout <= 0 {
		return fmt.Errorf("invalid timeout: %d", c.Serial.Timeout)
	}
	switch c.Mode {
	case "server", "client":
	default:
		return fmt.Errorf("invalid mode %q (expected server or client)", c.Mode)
	}
	if c.Address == "" {
		return fmt.Errorf("address is required")
	}
	if c.MaxConn < 0 {
		return fmt.Errorf("invalid max_conn: %d", c.MaxConn)
	}
	return nil
}

func (s *SerialConfig) toMode() *serial.Mode {
	return &serial.Mode{
		BaudRate: s.BaudRate,
		DataBits: s.DataBits,
		StopBits: s.toStopBits(),
		Parity:   s.toParity(),
	}
}

func (s *SerialConfig) toParity() serial.Parity {
	switch s.Parity {
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
