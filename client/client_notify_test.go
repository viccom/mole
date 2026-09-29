package moleAgent_client

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestNotifyManagersDoesNotLogSensitiveConfig(t *testing.T) {
	enabled := true
	client := &Client{}

	oldWriter := log.Writer()
	oldFlags := log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer log.SetOutput(oldWriter)
	defer log.SetFlags(oldFlags)

	client.notifyManagers([]Tunnel{
		{
			Name:    "serial-secret",
			Type:    TunnelTypeSer2MQ,
			Enabled: &enabled,
			Para: []byte(`{
				"broker":"mqtt://user:pass@example.com:1883",
				"secret":"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
				"serial":{"port":"COM3"}
			}`),
		},
		{
			Name:    "vpn-secret",
			Type:    TunnelTypeVPNMgr,
			Enabled: &enabled,
			Para: []byte(`{
				"vnt":{"token":"token-123","password":"vpn-pass"}
			}`),
		},
	})

	output := buf.String()
	for _, secret := range []string{
		"user:pass@example.com",
		"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		"token-123",
		"vpn-pass",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("notifyManagers() log leaked sensitive value %q in output %q", secret, output)
		}
	}
}
