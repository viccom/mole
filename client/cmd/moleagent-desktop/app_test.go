package main

import (
	"encoding/json"
	"strings"
	"testing"

	moleAgent_client "moleAgent_client"
	"moleAgent_client/cmd/moleagent-desktop/internal/node"
)

func newTestDesktopApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("APPDATA", t.TempDir())

	app := NewApp()
	app.nodeMgr = node.NewManager()
	app.nodeMgrAPI = node.NewNodeManagerAPI(app.nodeMgr)
	app.builtinPort = moleAgent_client.DefaultConfig().BuiltinHTTP
	return app
}

func TestSwitchNodeKeepsCurrentClientWhenNewNodeConfigIsInvalid(t *testing.T) {
	app := newTestDesktopApp(t)

	if err := app.nodeMgr.Add(node.Node{
		Name:       "stable",
		NodeName:   "stable-node",
		ServerAddr: "127.0.0.1:9981",
		Token:      "token-a",
		Transport:  "tcp",
	}); err != nil {
		t.Fatalf("add stable node: %v", err)
	}
	if err := app.nodeMgr.Add(node.Node{
		Name:       "broken",
		NodeName:   "broken-node",
		ServerAddr: "127.0.0.1:9981",
		Token:      "token-b",
		Transport:  "broken",
	}); err != nil {
		t.Fatalf("add broken node: %v", err)
	}

	nodes := app.nodeMgr.List()
	stable := nodes[0]
	broken := nodes[1]

	if err := app.startClientWithNode(&stable); err != nil {
		t.Fatalf("start stable client: %v", err)
	}
	t.Cleanup(app.stopClient)

	app.nodeMgr.SetCurrentNode(stable.ID)
	if got := app.currentClient(); got == nil {
		t.Fatal("expected current client before switch")
	}

	err := app.switchNode(broken.ID)
	if err == nil {
		t.Fatal("expected switchNode to fail for invalid transport")
	}
	if !strings.Contains(err.Error(), "invalid transport") {
		t.Fatalf("switchNode error = %v, want invalid transport", err)
	}

	current := app.nodeMgr.GetCurrentNode()
	if current == nil || current.ID != stable.ID {
		t.Fatalf("current node changed after failed switch: %#v", current)
	}
	if got := app.currentClient(); got == nil {
		t.Fatal("expected previous client to remain active after failed switch")
	}
}

func TestAddNodeRejectsInvalidTransport(t *testing.T) {
	app := newTestDesktopApp(t)

	resp := app.AddNode(`{"name":"bad","node_name":"bad-node","server_addr":"127.0.0.1:9981","token":"token","transport":"broken"}`)

	var payload map[string]string
	if err := json.Unmarshal([]byte(resp), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !strings.Contains(payload["error"], "invalid transport") {
		t.Fatalf("AddNode error = %q, want invalid transport", payload["error"])
	}
	if got := len(app.nodeMgr.List()); got != 0 {
		t.Fatalf("node count = %d, want 0", got)
	}
}

func TestSetBuiltinHTTPPortRejectsInvalidAddress(t *testing.T) {
	app := newTestDesktopApp(t)
	original := app.builtinPort
	originalConfigPort := app.nodeMgr.GetBuiltinHTTP()

	if err := app.setBuiltinHTTPPort("not-an-addr"); err == nil {
		t.Fatal("expected invalid builtin HTTP address error")
	}
	if got := app.builtinPort; got != original {
		t.Fatalf("builtin port changed after invalid update: got %q want %q", got, original)
	}
	if got := app.nodeMgr.GetBuiltinHTTP(); got != originalConfigPort {
		t.Fatalf("config builtin HTTP changed unexpectedly: got %q want %q", got, originalConfigPort)
	}
}

func TestGetShellInfoReturnsDesktopModeAndLocalUIURL(t *testing.T) {
	app := newTestDesktopApp(t)
	app.builtinPort = "127.0.0.1:59870"

	raw := app.GetShellInfo()

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal shell info: %v", err)
	}
	if got, want := payload["mode"], "desktop"; got != want {
		t.Fatalf("mode = %v, want %q", got, want)
	}
	if got, want := payload["ui_url"], "http://127.0.0.1:59870/ui"; got != want {
		t.Fatalf("ui_url = %v, want %q", got, want)
	}
}
