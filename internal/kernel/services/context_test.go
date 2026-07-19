package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	contextmarkdown "github.com/nigelteosw/eggy/internal/adapters/context/markdown"
	"github.com/nigelteosw/eggy/internal/ports"
)

func TestSecretGuardRejectsCredentials(t *testing.T) {
	guard := NewSecretGuard([]string{"exact-active-secret"})
	for _, input := range []struct{ section, content string }{
		{"Credentials", "ordinary"}, {"Notes", "github_pat_abcdefghijklmnopqrstuvwxyz"}, {"Notes", "Bearer abcdef"},
		{"Notes", "password=hunter2"}, {"Notes", "-----BEGIN PRIVATE KEY-----"}, {"Notes", "exact-active-secret"},
	} {
		if err := guard.Validate(input.section, input.content); err == nil || !strings.Contains(err.Error(), "secret") {
			t.Fatalf("section=%q content=%q error=%v", input.section, input.content, err)
		}
	}
	if err := guard.Validate("Preferences", "Use repository eggy by default"); err != nil {
		t.Fatal(err)
	}
}

func TestContextToolsCurateUserAndMemory(t *testing.T) {
	store := contextmarkdown.Open(t.TempDir(), 64<<10)
	tools := NewContextTools(store, NewSecretGuard([]string{"secret-value"}))
	byName := map[string]ports.Tool{}
	for _, tool := range tools {
		byName[tool.Definition().Name] = tool
	}
	if len(byName) != 4 {
		t.Fatalf("tools=%v", byName)
	}
	result, err := byName["user_append"].Execute(context.Background(), json.RawMessage(`{"section":"Preferences","content":"Concise"}`))
	if err != nil || string(result) != `{"updated":true}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	if _, err := byName["memory_append"].Execute(context.Background(), json.RawMessage(`{"section":"Credentials","content":"secret-value"}`)); err == nil {
		t.Fatal("expected secret rejection")
	}
	loaded, err := store.Load(context.Background())
	if err != nil || !strings.Contains(loaded.User, "Concise") || strings.Contains(loaded.Memory, "secret-value") {
		t.Fatalf("context=%#v err=%v", loaded, err)
	}
}
