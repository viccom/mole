package tunnel

import (
	"testing"
)

func TestParseVirtualHostByHyphen(t *testing.T) {
	tests := []struct {
		host        string
		wantClient  string
		wantMapping string
		wantVhost   bool
	}{
		// 泛域名格式: mappingName-clientId.domain
		{"api-node001.example.com", "node001", "api", true},
		{"web-server01.example.com", "server01", "web", true},
		{"app-server02.test.example.com", "server02", "app", true},
		// 带端口
		{"api-node001.example.com:8080", "node001", "api", true},
		// localhost 格式
		{"api-node001.localhost", "node001", "api", true},  // hostParts=["api-node001", "localhost"] - 2 parts
		// 非泛域名
		{"example.com", "", "", false},
		{"api.example.com", "", "", false}, // 只有两个部分
		{"192.168.1.1", "", "", false},      // IP地址
	}

	for _, tt := range tests {
		clientId, mappingName, isVhost := parseVirtualHostByHyphen(tt.host)
		if clientId != tt.wantClient || mappingName != tt.wantMapping || isVhost != tt.wantVhost {
			t.Errorf("parseVirtualHostByHyphen(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.host, clientId, mappingName, isVhost, tt.wantClient, tt.wantMapping, tt.wantVhost)
		}
	}
}

func TestParseVirtualHost(t *testing.T) {
	tests := []struct {
		host        string
		wantClient  string
		wantMapping string
		wantVhost   bool
	}{
		// 泛域名格式: mappingName.clientId.domain
		{"api.node001.example.com", "node001", "api", true},
		{"web.server01.example.com", "server01", "web", true},
		// 带端口
		{"api.node001.example.com:8080", "node001", "api", true},
		// localhost 格式
		{"api.node001.localhost", "node001", "api", true},
		// 非泛域名
		{"example.com", "", "", false},
		{"api.example.com", "", "", false}, // 只有两个部分
	}

	for _, tt := range tests {
		clientId, mappingName, isVhost := parseVirtualHost(tt.host)
		if clientId != tt.wantClient || mappingName != tt.wantMapping || isVhost != tt.wantVhost {
			t.Errorf("parseVirtualHost(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.host, clientId, mappingName, isVhost, tt.wantClient, tt.wantMapping, tt.wantVhost)
		}
	}
}

func TestParsePathRoute(t *testing.T) {
	tests := []struct {
		path        string
		wantClient  string
		wantMapping string
		wantErr     bool
	}{
		{"/node001/api", "node001", "api", false},
		{"/node001/api/v1/sysinfo", "node001", "api", false},
		{"/server01/web/static", "server01", "web", false},
		// 根路径
		{"/", "", "", true},
		// 只有一级路径
		{"/node001", "", "", true},
	}

	for _, tt := range tests {
		clientId, mappingName, err := parsePathRoute(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("parsePathRoute(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && (clientId != tt.wantClient || mappingName != tt.wantMapping) {
			t.Errorf("parsePathRoute(%q) = (%q, %q), want (%q, %q)",
				tt.path, clientId, mappingName, tt.wantClient, tt.wantMapping)
		}
	}
}

func TestBuildPathRoutePath(t *testing.T) {
	tests := []struct {
		original string
		want     string
	}{
		{"/node001/api", "/api"},
		{"/node001/api/v1/sysinfo", "/api/v1/sysinfo"},
		{"/server01/web", "/web"},
	}

	for _, tt := range tests {
		got := buildPathRoutePath(tt.original)
		if got != tt.want {
			t.Errorf("buildPathRoutePath(%q) = %q, want %q", tt.original, got, tt.want)
		}
	}
}
