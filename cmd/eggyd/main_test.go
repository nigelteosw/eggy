package main

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestNewSetupModeCreatesIndependentExpiringCredentials(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	mode, token, err := newSetupMode("/tmp/home", "/tmp/home/config.yaml", "https://eggy.example", func(string) string { return "" }, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("token length=%d err=%v", len(decoded), err)
	}
	if mode.TokenHash != sha256.Sum256([]byte(token)) {
		t.Fatal("mode did not hash the displayed token")
	}
	if len(mode.SessionKey) != 32 || string(mode.SessionKey) == token {
		t.Fatal("setup session key is missing or reused")
	}
	if !mode.Expires.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("expires=%s", mode.Expires)
	}
}

func TestSetupPublicBaseURLUsesTrustedEnvironmentOnly(t *testing.T) {
	getenv := func(name string) string {
		return map[string]string{"RAILWAY_PUBLIC_DOMAIN": "eggy.up.railway.app", "FORWARDED_HOST": "evil.example"}[name]
	}
	if got := setupPublicBaseURL(getenv); got != "https://eggy.up.railway.app" {
		t.Fatalf("URL=%q", got)
	}
	if got := setupURL("https://eggy.example", "secret-token"); got != "https://eggy.example/#setup=secret-token" || strings.Contains(got, "?") {
		t.Fatalf("setup URL=%q", got)
	}
}
