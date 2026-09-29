package node

import "testing"

func TestAddAssignsStableID(t *testing.T) {
	mgr := NewManager()
	err := mgr.Add(Node{
		Name:       "服务器A",
		ServerAddr: "127.0.0.1:9981",
		Token:      "token-a",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	nodes := mgr.List()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].ID == "" {
		t.Fatal("expected generated node ID")
	}
}

func TestUpdateAllowsRenamingAliasWhileKeepingCurrentNodeByID(t *testing.T) {
	mgr := NewManager()
	err := mgr.Add(Node{
		Name:       "旧别名",
		NodeName:   "client-a",
		ServerAddr: "127.0.0.1:9981",
		Token:      "token-a",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	original := mgr.List()[0]
	mgr.SetCurrentNode(original.ID)

	err = mgr.Update(original.ID, Node{
		ID:         original.ID,
		Name:       "新别名",
		NodeName:   "client-b",
		ServerAddr: "127.0.0.1:9982",
		Token:      "token-b",
		Transport:  "ws",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	current := mgr.GetCurrentNode()
	if current == nil {
		t.Fatal("expected current node")
	}
	if current.ID != original.ID {
		t.Fatalf("current node ID changed: got %q want %q", current.ID, original.ID)
	}
	if current.Name != "新别名" {
		t.Fatalf("alias rename did not persist: got %q", current.Name)
	}
	if current.NodeName != "client-b" {
		t.Fatalf("node_name update did not persist: got %q", current.NodeName)
	}
}
