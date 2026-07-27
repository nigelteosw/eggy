package mcp

import (
	"bytes"
	"encoding/base64"
	"os"
	"testing"
)

func TestOAuthStoreRoundTripIsEncryptedAndAtomic(t *testing.T) {
	store, err := OpenOAuthStore(t.TempDir(), testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	record := OAuthRecord{Version: 1, ServerURL: "https://mcp.example", ClientID: "client", AccessToken: "access-secret", RefreshToken: "refresh-secret"}
	if err := store.Save("railway", record.ServerURL, record); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.path("railway"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("refresh-secret")) || bytes.Contains(raw, []byte("client")) {
		t.Fatalf("credential written in plaintext: %s", raw)
	}
	info, err := os.Stat(store.path("railway"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	got, err := store.Load("railway", record.ServerURL)
	if err != nil || got.RefreshToken != record.RefreshToken || got.ClientID != record.ClientID {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestOAuthStoreUsesRandomNoncesAndRejectsTampering(t *testing.T) {
	store, err := OpenOAuthStore(t.TempDir(), testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	record := OAuthRecord{Version: 1, ServerURL: "https://mcp.example", RefreshToken: "secret"}
	if err := store.Save("railway", record.ServerURL, record); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(store.path("railway"))
	if err := store.Save("railway", record.ServerURL, record); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(store.path("railway"))
	if bytes.Equal(first, second) {
		t.Fatal("encrypted records reused a nonce")
	}
	second[len(second)-1] ^= 1
	if err := os.WriteFile(store.path("railway"), second, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("railway", record.ServerURL); err == nil {
		t.Fatal("tampered OAuth record was accepted")
	}
}

func TestOAuthStoreBindsRecordToServerURLAndDeletes(t *testing.T) {
	store, err := OpenOAuthStore(t.TempDir(), testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	record := OAuthRecord{Version: 1, ServerURL: "https://one.example", RefreshToken: "secret"}
	if err := store.Save("railway", record.ServerURL, record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("railway", "https://two.example"); err == nil {
		t.Fatal("OAuth record loaded for a different server URL")
	}
	if err := store.Delete("railway", record.ServerURL); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("railway", record.ServerURL); err == nil {
		t.Fatal("deleted OAuth record still loads")
	}
}

func testEncryptionKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
}
