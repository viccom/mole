package main

import (
	"encoding/json"
	"testing"

	"moleAgent_client/cmd/moleagent-manager/internal/node"
)

func TestErrorJSONEscapesQuotes(t *testing.T) {
	t.Parallel()

	payload := errorJSON(`node "alpha" is offline`)

	var decoded map[string]string
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("errorJSON returned invalid JSON: %v", err)
	}
	if got, want := decoded["error"], `node "alpha" is offline`; got != want {
		t.Fatalf("decoded error = %q, want %q", got, want)
	}
}

func TestGetCurrentNodeURLReturnsAboutBlankWhenCurrentNodeIsOffline(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	app := NewApp()
	app.nodeMgr = node.NewManager()
	if err := app.nodeMgr.Add(node.Node{
		Name:      "offline",
		Addr:      "127.0.0.1",
		Port:      1,
		IsDefault: true,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	app.nodeMgr.SetCurrentNode("offline")

	if got := app.GetCurrentNodeURL(); got != "about:blank" {
		t.Fatalf("GetCurrentNodeURL() = %q, want %q", got, "about:blank")
	}
}

func TestGetShellInfoReturnsManagerMode(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	app := NewApp()
	app.nodeMgr = node.NewManager()

	raw := app.GetShellInfo()

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal shell info: %v", err)
	}
	if got, want := payload["mode"], "manager"; got != want {
		t.Fatalf("mode = %v, want %q", got, want)
	}
	if got, want := payload["ui_url"], "about:blank"; got != want {
		t.Fatalf("ui_url = %v, want %q", got, want)
	}
}
