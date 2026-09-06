package mcp

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestOAuthStoreRoundTripIsEncrypted(t *testing.T) {
	records := newMemoryRecords()
	store, err := OpenOAuthStore(records, testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	record := OAuthRecord{Version: 1, ServerURL: "https://mcp.example", ClientID: "client", AccessToken: "access-secret", RefreshToken: "refresh-secret"}
	if err := store.Save("railway", record.ServerURL, record); err != nil {
		t.Fatal(err)
	}
	raw := records.raw(oauthSection, "railway")
	if bytes.Contains(raw, []byte("refresh-secret")) || bytes.Contains(raw, []byte("client")) {
		t.Fatalf("credential written in plaintext: %s", raw)
	}
	got, err := store.Load("railway", record.ServerURL)
	if err != nil || got.RefreshToken != record.RefreshToken || got.ClientID != record.ClientID {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestOAuthStoreUsesRandomNoncesAndRejectsTampering(t *testing.T) {
	records := newMemoryRecords()
	store, err := OpenOAuthStore(records, testEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	record := OAuthRecord{Version: 1, ServerURL: "https://mcp.example", RefreshToken: "secret"}
	if err := store.Save("railway", record.ServerURL, record); err != nil {
		t.Fatal(err)
	}
	first := records.raw(oauthSection, "railway")
	if err := store.Save("railway", record.ServerURL, record); err != nil {
		t.Fatal(err)
	}
	second := records.raw(oauthSection, "railway")
	if bytes.Equal(first, second) {
		t.Fatal("encrypted records reused a nonce")
	}
	second[len(second)-2] ^= 1
	records.put(oauthSection, "railway", second)
	if _, err := store.Load("railway", record.ServerURL); err == nil {
		t.Fatal("tampered OAuth record was accepted")
	}
}

func TestOAuthStoreBindsRecordToServerURLAndDeletes(t *testing.T) {
	store, err := OpenOAuthStore(newMemoryRecords(), testEncryptionKey())
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
