package vpn

import (
	"context"
	"reflect"
	"testing"
)

func TestOnTunnelUpdateRefreshesExistingStoppedProcessConfig(t *testing.T) {
	manager := NewManager(context.Background())

	oldCfg := Config{
		Binary: BinaryConfig{Name: "vnt-cli-old"},
		Args:   []string{"-k", "old-token"},
	}
	newCfg := Config{
		Binary: BinaryConfig{Name: "vnt-cli-new"},
		Args:   []string{"-k", "new-token"},
	}

	manager.configs["vpn-a"] = oldCfg
	manager.procs["vpn-a"] = &ProcessMgr{
		name: "vpn-a",
		cfg:  oldCfg,
	}

	manager.OnTunnelUpdate(map[string]Config{
		"vpn-a": newCfg,
	})

	got := manager.procs["vpn-a"]
	if got == nil {
		t.Fatal("OnTunnelUpdate() should keep a process manager entry for existing stopped tunnel")
	}
	if !reflect.DeepEqual(got.cfg, newCfg) {
		t.Fatalf("OnTunnelUpdate() process config = %#v, want %#v", got.cfg, newCfg)
	}
	if !reflect.DeepEqual(manager.configs["vpn-a"], newCfg) {
		t.Fatalf("OnTunnelUpdate() stored config = %#v, want %#v", manager.configs["vpn-a"], newCfg)
	}
}
