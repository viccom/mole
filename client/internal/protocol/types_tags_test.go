package protocol

import (
	"reflect"
	"strings"
	"testing"

	"mole/shared/proto"
)

// TestTunnelWireFieldsLocked 锁定 protocol.Tunnel 的「字段名:json tag」序列
// 与 proto.TunnelWireFields 契约逐字一致（字段【类型】允许分叉——本包
// RateLimit 为 json.RawMessage 透传，server 端为 *TunnelRateLimit 结构化——
// 字段名与 tag 才决定 wire 兼容）。任一端加/改字段而漏改契约（或反之）时
// 本测试红，防止 JSON 反序列化静默丢字段。契约变更流程见
// proto.TunnelWireFields 文档注释。
func TestTunnelWireFieldsLocked(t *testing.T) {
	typ := reflect.TypeOf(Tunnel{})
	got := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		got = append(got, f.Name+":"+f.Tag.Get("json"))
	}
	if !reflect.DeepEqual(got, proto.TunnelWireFields) {
		t.Fatalf("protocol.Tunnel wire 字段契约漂移（got ← 本地 struct，want ← proto.TunnelWireFields）:\ngot:\n  %s\nwant:\n  %s",
			strings.Join(got, "\n  "), strings.Join(proto.TunnelWireFields, "\n  "))
	}
}
