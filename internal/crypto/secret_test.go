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
		decrypted := enc.Decrypt(ciphertext)
		if decrypted != plain {
			t.Errorf("Decrypt(Encrypt(%q)) = %q", plain, decrypted)
		}
	}
}

func TestDecryptPassthrough(t *testing.T) {
	enc, _ := NewSecretEncryptor("test-secret")
	// Already plain text should pass through
	if got := enc.Decrypt("plain-password"); got != "plain-password" {
		t.Errorf("Decrypt(plain) = %q, want passthrough", got)
	}
	// Invalid base64 should pass through
	if got := enc.Decrypt("enc:!!!invalid!!!"); got != "enc:!!!invalid!!!" {
		t.Errorf("Decrypt(invalid) = %q, want passthrough", got)
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
