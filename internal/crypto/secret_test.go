package crypto

import "testing"

func TestEncryptDecrypt(t *testing.T) {
	enc, err := NewSecretEncryptor("test-master-secret-for-unit-tests")
	if err != nil {
		t.Fatal(err)
	}

	cases := []string{
		"hello world",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc123\n-----END OPENSSH PRIVATE KEY-----",
		"",
		"special chars: !@#$%^&*()",
	}
	for _, plain := range cases {
		if plain == "" {
			// empty should stay empty
			if got := enc.Encrypt(plain); got != "" {
				t.Errorf("Encrypt(%q) = %q, want empty", plain, got)
			}
			continue
		}
		ciphertext := enc.Encrypt(plain)
		if !IsEncrypted(ciphertext) {
			t.Errorf("Encrypt(%q) = %q, missing prefix", plain, ciphertext)
		}
		decrypted, derr := enc.Decrypt(ciphertext)
		if derr != nil {
			t.Errorf("Decrypt(Encrypt(%q)) error: %v", plain, derr)
		}
		if decrypted != plain {
			t.Errorf("Decrypt(Encrypt(%q)) = %q", plain, decrypted)
		}
	}
}

func TestDecryptPassthrough(t *testing.T) {
	enc, _ := NewSecretEncryptor("test-secret")
	// Plain text (no enc: prefix) passes through with no error
	got, err := enc.Decrypt("plain-password")
	if err != nil || got != "plain-password" {
		t.Errorf("Decrypt(plain) = (%q, %v), want (plain-password, nil)", got, err)
	}
	// Invalid base64 under enc: prefix must now return an error (no silent passthrough)
	if _, err := enc.Decrypt("enc:!!!invalid!!!"); err == nil {
		t.Error("Decrypt(invalid base64) should return error, got nil")
	}
	// Wrong secret must surface an error, not return ciphertext
	other, _ := NewSecretEncryptor("different-secret")
	ct := enc.Encrypt("secret-data")
	if _, err := other.Decrypt(ct); err == nil {
		t.Error("Decrypt with wrong secret should return error, got nil")
	}
}

func TestIsEncrypted(t *testing.T) {
	if IsEncrypted("plain") {
		t.Error("IsEncrypted(plain) should be false")
	}
	if !IsEncrypted("enc:dGVzdA==") {
		t.Error("IsEncrypted(enc:...) should be true")
	}
}
