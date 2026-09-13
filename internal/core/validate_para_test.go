package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func p2pParaJSON(t *testing.T, s string) json.RawMessage {
	t.Helper()
	return json.RawMessage(s)
}

// p2p Para 校验是 p2p 隧道落库前的第一道闸：room 是共享密钥材料
// （知道 room 即可加入信令并推导 payload key），格式必须严格把关；
// 与客户端 internal/proxy/p2p/config.go 的 Validate 保持同一套规则。
func TestValidateP2PPara(t *testing.T) {
	validRoom := "abcd1234ABCD_-"

	tests := []struct {
		name    string
		para    string
		wantErr bool
	}{
		{"发起端合法（带 modes）", `{"room":"` + validRoom + `","modes":["lan","tcp-v6"],"protocol":"tcp","local_port":18080,"target_host":"127.0.0.1","target_port":8080}`, false},
		{"发起端合法（modes 缺省）", `{"room":"` + validRoom + `","protocol":"udp","local_port":1,"target_host":"192.168.1.2","target_port":65535}`, false},
		{"纯会话端合法（无 target）", `{"room":"` + validRoom + `","modes":["v4-relay","tcp-v4"],"protocol":"tcp"}`, false},
		{"纯会话端合法（target_port=0）", `{"room":"` + validRoom + `","protocol":"udp","target_port":0}`, false},
		{"room 过短（7 字符）", `{"room":"abcd123","protocol":"tcp"}`, true},
		{"room 过长（33 字符）", `{"room":"` + strings.Repeat("a", 33) + `","protocol":"tcp"}`, true},
		{"room 含非法字符（空格）", `{"room":"abcd 1234","protocol":"tcp"}`, true},
		{"room 含非法字符（中文）", `{"room":"房间号abcd1234","protocol":"tcp"}`, true},
		{"room 缺失", `{"protocol":"tcp"}`, true},
		{"room 含非法字符（感叹号）", `{"room":"abcd1234!","protocol":"tcp"}`, true},
		{"modes 未知模式", `{"room":"` + validRoom + `","modes":["lan","tcp-v5"],"protocol":"tcp"}`, true},
		{"modes 含 relay 之外的非法值", `{"room":"` + validRoom + `","modes":["UDP-V4"],"protocol":"tcp"}`, true},
		{"protocol 缺失", `{"room":"` + validRoom + `"}`, true},
		{"protocol 非法值", `{"room":"` + validRoom + `","protocol":"sctp"}`, true},
		{"protocol 大小写敏感", `{"room":"` + validRoom + `","protocol":"TCP"}`, true},
		{"发起端 local_port 越下界", `{"room":"` + validRoom + `","protocol":"tcp","local_port":0,"target_host":"h","target_port":80}`, true},
		{"发起端 local_port 越上界", `{"room":"` + validRoom + `","protocol":"tcp","local_port":65536,"target_host":"h","target_port":80}`, true},
		{"发起端 target_port 缺失", `{"room":"` + validRoom + `","protocol":"tcp","local_port":8080,"target_host":"h"}`, true},
		{"发起端 target_port 越上界", `{"room":"` + validRoom + `","protocol":"tcp","local_port":8080,"target_host":"h","target_port":70000}`, true},
		{"纯会话端 target_port 非 0", `{"room":"` + validRoom + `","protocol":"tcp","target_port":8080}`, true},
		{"para 非 JSON", `not-json`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateP2PPara(p2pParaJSON(t, tt.para))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateP2PPara(%s) error = %v, wantErr %v", tt.para, err, tt.wantErr)
			}
		})
	}
}

// nil Para 必须拒绝：p2p 隧道没有 room 无法配对，等价于配置缺失
func TestValidateP2PParaNilPara(t *testing.T) {
	if err := ValidateP2PPara(nil); err == nil {
		t.Fatal("nil para must be rejected")
	}
}

// P2PRoom 供 service 层配对校验提取 room，解析失败必须显式报错而非返回空串
func TestP2PRoom(t *testing.T) {
	room, err := P2PRoom(json.RawMessage(`{"room":"abcd1234xyz","protocol":"tcp"}`))
	if err != nil {
		t.Fatalf("P2PRoom: %v", err)
	}
	if room != "abcd1234xyz" {
		t.Fatalf("P2PRoom = %q", room)
	}
	if _, err := P2PRoom(json.RawMessage(`{"protocol":"tcp"}`)); err == nil {
		t.Fatal("missing room must error")
	}
	if _, err := P2PRoom(nil); err == nil {
		t.Fatal("nil para must error")
	}
}
