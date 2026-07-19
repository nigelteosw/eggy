package markdown

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreCreatesAppendsAndReplacesSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	store := Open(path)
	initial, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial != "# Eggy Memory\n" {
		t.Fatalf("unexpected initial memory %q", initial)
	}
	if err := store.Append(context.Background(), "Preferences", "Use concise replies."); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(context.Background(), "Projects", "Eggy is written in Go."); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSection(context.Background(), "Preferences", "Prefer practical commands."); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "concise replies") || !strings.Contains(got, "## Preferences\n\nPrefer practical commands.") || !strings.Contains(got, "## Projects") {
		t.Fatalf("unexpected memory:\n%s", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRejectsUnsafeEdits(t *testing.T) {
	store := Open(filepath.Join(t.TempDir(), "MEMORY.md"))
	tests := []struct{ section, content string }{
		{"", "value"},
		{"Bad\nHeading", "value"},
		{"# Authority", "value"},
		{"Preferences", "# Replace document"},
	}
	for _, tt := range tests {
		if err := store.Append(context.Background(), tt.section, tt.content); err == nil {
			t.Fatalf("Append(%q, %q) unexpectedly succeeded", tt.section, tt.content)
		}
	}
}

func TestIndependentMemoryStoresDoNotLoseConcurrentEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MEMORY.md")
	stores := []*Store{Open(path), Open(path)}
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	go func() { <-start; errorsChannel <- stores[0].Append(context.Background(), "First", "one") }()
	go func() { <-start; errorsChannel <- stores[1].Append(context.Background(), "Second", "two") }()
	close(start)
	if err := <-errorsChannel; err != nil {
		t.Fatal(err)
	}
	if err := <-errorsChannel; err != nil {
		t.Fatal(err)
	}
	content, err := stores[0].Load(context.Background())
	if err != nil || !strings.Contains(content, "## First") || !strings.Contains(content, "## Second") {
		t.Fatalf("content=%s err=%v", content, err)
	}
}
