package moleAgent_client

import "testing"

func TestApplyTunnelMutationAddUsesLatestSnapshot(t *testing.T) {
	current := []Tunnel{
		{Name: "alpha", Type: TunnelTypeTCP, Target: "127.0.0.1:1000"},
	}
	next, err := applyTunnelMutation(current, tunnelMutation{
		kind: tunnelMutationAdd,
		tunnel: Tunnel{Name: "beta", Type: TunnelTypeTCP, Target: "127.0.0.1:2000"},
	})
	if err != nil {
		t.Fatalf("applyTunnelMutation(add) error = %v", err)
	}
	if len(next) != 2 {
		t.Fatalf("applyTunnelMutation(add) len = %d, want 2", len(next))
	}
	if next[0].Name != "alpha" || next[1].Name != "beta" {
		t.Fatalf("applyTunnelMutation(add) result = %#v", next)
	}
}

func TestApplyTunnelMutationRemoveMissingTunnelReturnsError(t *testing.T) {
	_, err := applyTunnelMutation([]Tunnel{
		{Name: "alpha", Type: TunnelTypeTCP, Target: "127.0.0.1:1000"},
	}, tunnelMutation{
		kind: tunnelMutationRemove,
		name: "beta",
	})
	if err == nil {
		t.Fatal("applyTunnelMutation(remove) should fail when tunnel is missing")
	}
}
