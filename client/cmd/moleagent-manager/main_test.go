package main

import "testing"

func TestEmbeddedTrayIconPresent(t *testing.T) {
	if len(iconData) == 0 {
		t.Fatal("expected embedded manager tray icon data")
	}
}
