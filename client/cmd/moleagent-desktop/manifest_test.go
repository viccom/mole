package main

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsManifestRequiresAdministrator(t *testing.T) {
	data, err := os.ReadFile("build/windows/wails.exe.manifest")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "requestedExecutionLevel") || !strings.Contains(content, `level="requireAdministrator"`) {
		t.Fatal("expected Windows manifest to require administrator privileges")
	}
}
