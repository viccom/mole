package tunnel

import (
	"encoding/json"
	"io"
	"strings"
)

// biCopy 双向转发数据，任一方向完成后等待另一方向完成
func biCopy(a, b io.ReadWriter) {
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(a, b)
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(b, a)
	}()
	<-done
	<-done
}

// writeJSONLine 将对象序列化为 JSON 并写入，末尾加换行符
func writeJSONLine(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// stripPort 去除 host 中的端口号部分
func stripPort(host string) string {
	if idx := strings.Index(host, ":"); idx != -1 {
		return host[:idx]
	}
	return host
}
