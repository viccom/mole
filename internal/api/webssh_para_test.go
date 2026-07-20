package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func genTestRSAPrivKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

func mustParaJSON(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal para: %v", err)
	}
	return b
}

func TestValidateWebSSHPara(t *testing.T) {
	validPEM := genTestRSAPrivKeyPEM(t)

	tests := []struct {
		name    string
		para    map[string]any
		wantErr bool
		errSub  string
	}{
		{
			name:    "合法 RSA 私钥通过",
			para:    map[string]any{"auth_type": "key", "priv_key": validPEM},
			wantErr: false,
		},
		{
			name:    "公钥误填为私钥应拒绝",
			para:    map[string]any{"auth_type": "key", "priv_key": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQAB test@example"},
			wantErr: true,
			errSub:  "不是合法私钥",
		},
		{
			name:    "密文 priv_key 跳过格式校验",
			para:    map[string]any{"auth_type": "key", "priv_key": "enc:YWJjZGVm"},
			wantErr: false,
		},
		{
			name:    "key 认证缺 priv_key 应拒绝",
			para:    map[string]any{"auth_type": "key", "priv_key": ""},
			wantErr: true,
			errSub:  "priv_key is required",
		},
		{
			name:    "password 认证通过",
			para:    map[string]any{"auth_type": "password", "password": "secret"},
			wantErr: false,
		},
		{
			name:    "非法 auth_type 应拒绝",
			para:    map[string]any{"auth_type": "foo"},
			wantErr: true,
			errSub:  "auth_type must be",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebSSHPara(mustParaJSON(t, tt.para))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际 nil")
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("错误信息不含 %q: %v", tt.errSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望通过，实际报错: %v", err)
			}
		})
	}
}

func TestValidateWebSSHPara_InvalidJSON(t *testing.T) {
	if err := validateWebSSHPara(json.RawMessage(`{bad json`)); err == nil {
		t.Fatal("期望 JSON 解析报错，实际 nil")
	}
}
