package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNodeAuthLineWireShape 锁认证行 wire 契约（方案 B enc 请求位）。
// 认证行自此单源为 NodeAuthLine struct（此前服务端匿名 struct / 客户端 map
// 各自构造，加 enc 字段必然三处漂移且值类型错配——map 只能发字符串 "1"，
// int 字段收 1）。锁的是：三种新旧形态的字节级输出与缺席兼容。
func TestNodeAuthLineWireShape(t *testing.T) {
	// 新客户端 proof 格式 + enc 请求位
	b, err := json.Marshal(NodeAuthLine{Proof: "aabb", Enc: EncProtocolV1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); got != `{"proof":"aabb","enc":1}` {
		t.Errorf("proof+enc = %s, want {\"proof\":\"aabb\",\"enc\":1}", got)
	}
	// 新客户端 enc=off：与现行输出逐字节一致（不含 enc 键）
	b, err = json.Marshal(NodeAuthLine{Proof: "aabb"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); got != `{"proof":"aabb"}` {
		t.Errorf("proof only = %s, want {\"proof\":\"aabb\"}", got)
	}
	// 旧客户端 legacy token 形态
	b, err = json.Marshal(NodeAuthLine{Token: "legacy-token"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); got != `{"token":"legacy-token"}` {
		t.Errorf("token only = %s, want {\"token\":\"legacy-token\"}", got)
	}
	// 旧 JSON 喂新 struct：无 enc 字段 → 零值（缺席兼容，不报错）
	var l NodeAuthLine
	if err := json.Unmarshal([]byte(`{"proof":"aabb"}`), &l); err != nil {
		t.Fatalf("unmarshal legacy shape: %v", err)
	}
	if l.Proof != "aabb" || l.Enc != 0 {
		t.Errorf("legacy shape parsed = %+v, want proof set / enc zero", l)
	}
	// key 常量锁值（map/测试锁值场景）
	if AuthKeyEnc != "enc" || EncProtocolV1 != 1 {
		t.Errorf("AuthKeyEnc=%q EncProtocolV1=%d, want \"enc\"/1", AuthKeyEnc, EncProtocolV1)
	}
}

// TestControlResponseEncField 锁 ok 应答 enc 能力宣告的 wire 形态：
// nil 时键缺席（旧语义零变化），设置时 {"v":1}。
func TestControlResponseEncField(t *testing.T) {
	b, err := json.Marshal(ControlResponse{Cmd: "ok", Msg: "authenticated"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"enc"`) {
		t.Errorf("nil Enc must omit the key, got %s", b)
	}
	b, err = json.Marshal(ControlResponse{Cmd: "ok", Msg: "authenticated", Enc: &EncCapability{V: EncProtocolV1}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"enc":{"v":1}`) {
		t.Errorf("set Enc must carry {\"v\":1}, got %s", b)
	}
	// 旧客户端解析器（未知字段忽略）喂新应答的逆命题：新解析器喂旧应答
	var r ControlResponse
	if err := json.Unmarshal([]byte(`{"cmd":"ok","msg":"authenticated"}`), &r); err != nil {
		t.Fatalf("unmarshal legacy response: %v", err)
	}
	if r.Enc != nil {
		t.Errorf("legacy response parsed Enc = %+v, want nil", r.Enc)
	}
}
