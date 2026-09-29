package version

import (
	"encoding/json"
	"testing"
)

// /api/version 必须携带 p2p 构建能力标志（前端据此提示 P2P 不可用）。
// 值随构建标签变化（p2p=true / 默认=false），此处只断言键存在且为布尔
func TestSystemInfoJSONHasP2PKey(t *testing.T) {
	b, err := json.Marshal(GetSystemInfo())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m["p2p"]
	if !ok {
		t.Fatalf("SystemInfo JSON must contain \"p2p\" key, got %s", b)
	}
	if _, isBool := v.(bool); !isBool {
		t.Fatalf("\"p2p\" must be bool, got %T", v)
	}
}
