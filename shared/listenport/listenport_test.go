package listenport

import "testing"

// 等价性锁定①：集合值。6 个保留端口逐一在内、抽样端口外值不在、集合
// 大小恰为 6——集合是双端共用的单一事实源，任何增删都会同时改变两端的
// 放行/拒绝行为，必须显式评审后才允许改动本测试。
func TestReservedPortsMembership(t *testing.T) {
	t.Parallel()

	for _, port := range []int{9980, 9981, 9982, 9983, 1882, 1883} {
		if _, reserved := ReservedPorts[port]; !reserved {
			t.Errorf("port %d 应在保留端口集合内", port)
		}
	}
	for _, port := range []int{80, 443, 9999} {
		if _, reserved := ReservedPorts[port]; reserved {
			t.Errorf("port %d 不应在保留端口集合内", port)
		}
	}
	if len(ReservedPorts) != 6 {
		t.Errorf("保留端口集合大小 = %d, want 6", len(ReservedPorts))
	}
}

// 等价性锁定②：两条错误文案逐字（字符串全等，不是 Contains）。server 侧
// 的 "invalid tunnel configuration: " 前缀由 core 适配层追加，不在此锁。
func TestValidateMessagesExact(t *testing.T) {
	t.Parallel()

	if err := Validate(70000); err == nil || err.Error() != "listen_port must be 1-65535, got 70000" {
		t.Errorf("区间文案漂移: got %v, want %q", err, "listen_port must be 1-65535, got 70000")
	}
	if err := Validate(9980); err == nil || err.Error() != "listen_port 9980 is reserved for the server itself" {
		t.Errorf("保留端口文案漂移: got %v, want %q", err, "listen_port 9980 is reserved for the server itself")
	}
}

// 等价性锁定③：边界。1/65535 合法、0/65536 非法。
func TestValidateBounds(t *testing.T) {
	t.Parallel()

	for _, port := range []int{1, 65535} {
		if err := Validate(port); err != nil {
			t.Errorf("port %d 应合法: %v", port, err)
		}
	}
	for _, port := range []int{0, 65536} {
		if err := Validate(port); err == nil {
			t.Errorf("port %d 应非法", port)
		}
	}
}
