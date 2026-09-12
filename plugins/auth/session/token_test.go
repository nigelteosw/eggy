package session

import (
	"strings"
	"testing"
)

func TestNewTokenIsRandomURLSafeAndHashesDeterministically(t *testing.T) {
	raw1, hash1, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	raw2, hash2, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw1 == raw2 || hash1 == hash2 {
		t.Fatal("two tokens collided")
	}
	if len(raw1) != 43 || strings.ContainsAny(raw1, "+/=") {
		t.Fatalf("raw token %q is not 32 bytes of URL-safe base64", raw1)
	}
	if HashToken(raw1) != hash1 || len(hash1) != 64 {
		t.Fatalf("hash %q does not match the token", hash1)
	}
	if strings.Contains(hash1, raw1) {
		t.Fatal("the hash contains the raw token")
	}
}
