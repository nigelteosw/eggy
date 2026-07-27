package google

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestTokenCipherRoundTripAndRandomNonce(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, err := NewTokenCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cipher.EncryptToken("refresh-secret")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := cipher.EncryptToken("refresh-secret")
	if first == second || strings.Contains(first, "refresh-secret") {
		t.Fatalf("ciphertexts are not randomized/redacted: %q %q", first, second)
	}
	plain, err := cipher.DecryptToken(first)
	if err != nil || plain != "refresh-secret" {
		t.Fatalf("round trip got %q, %v", plain, err)
	}
}

func TestTokenCipherRejectsBadKeyAndTampering(t *testing.T) {
	if _, err := NewTokenCipher(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("short key accepted")
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cipher, _ := NewTokenCipher(key)
	encoded, _ := cipher.EncryptToken("secret")
	raw, _ := base64.RawURLEncoding.DecodeString(encoded)
	raw[len(raw)-1] ^= 1
	if _, err := cipher.DecryptToken(base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
