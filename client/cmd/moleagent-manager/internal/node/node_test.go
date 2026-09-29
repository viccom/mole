package node

import "testing"

func TestAddExtractsPortFromAddressAndMarksFirstNodeDefault(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	if err := mgr.Add(Node{
		Name: "local",
		Addr: "http://127.0.0.1:19080/ui",
		Port: 18080,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	nodes := mgr.List()
	if len(nodes) != 1 {
		t.Fatalf("node count = %d, want 1", len(nodes))
	}
	if nodes[0].Addr != "127.0.0.1" {
		t.Fatalf("addr = %q, want %q", nodes[0].Addr, "127.0.0.1")
	}
	if nodes[0].Port != 19080 {
		t.Fatalf("port = %d, want 19080", nodes[0].Port)
	}
	if !nodes[0].IsDefault {
		t.Fatal("expected first node to become default")
	}
}

func TestAddAndUpdateKeepSingleDefaultNode(t *testing.T) {
	t.Parallel()

	mgr := NewManager()
	if err := mgr.Add(Node{Name: "node-a", Addr: "127.0.0.1", Port: 18080, IsDefault: true}); err != nil {
		t.Fatalf("Add node-a: %v", err)
	}
	if err := mgr.Add(Node{Name: "node-b", Addr: "127.0.0.2", Port: 18081, IsDefault: true}); err != nil {
		t.Fatalf("Add node-b: %v", err)
	}

	nodes := mgr.List()
	defaultCount := 0
	for _, n := range nodes {
		if n.IsDefault {
			defaultCount++
		}
	}
	if defaultCount != 1 {
		t.Fatalf("default count after add = %d, want 1", defaultCount)
	}
	if !mgr.GetNode("node-b").IsDefault {
		t.Fatal("expected last explicit default node to remain default")
	}

	if err := mgr.Update("node-a", Node{Name: "renamed", Addr: "127.0.0.1", Port: 18080, IsDefault: true}); err != nil {
		t.Fatalf("Update node-a: %v", err)
	}
	nodes = mgr.List()
	defaultCount = 0
	for _, n := range nodes {
		if n.IsDefault {
			defaultCount++
		}
	}
	if defaultCount != 1 {
		t.Fatalf("default count after update = %d, want 1", defaultCount)
	}
	if !mgr.GetNode("node-a").IsDefault {
		t.Fatal("expected updated node-a to become default")
	}
}

func TestGetURLSupportsIPv6Hosts(t *testing.T) {
	t.Parallel()

	n := &Node{Addr: "::1", Port: 18080}
	if got, want := n.GetURL(), "http://[::1]:18080/ui"; got != want {
		t.Fatalf("GetURL() = %q, want %q", got, want)
	}
	if got, want := n.GetHealthURL(), "http://[::1]:18080/health"; got != want {
		t.Fatalf("GetHealthURL() = %q, want %q", got, want)
	}
}
