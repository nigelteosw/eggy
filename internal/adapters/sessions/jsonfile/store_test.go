package jsonfile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestStoreReloadsSessionAndOrderedEvents(t *testing.T) {
	root := t.TempDir()
	store := Open(root)
	if _, err := store.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Status: ports.SessionCreated}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(context.Background(), "run-1", ports.ImplementationSessionEvent{Kind: ports.SessionToolResult, Message: "Inspected: README.md"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Open(root).Load(context.Background(), "run-1")
	if err != nil || loaded.ID != "run-1" {
		t.Fatalf("session=%#v err=%v", loaded, err)
	}
	body, err := os.ReadFile(filepath.Join(root, "run-1", "events.jsonl"))
	if err != nil || !strings.Contains(string(body), `"sequence":1`) || !strings.Contains(string(body), "Inspected: README.md") {
		t.Fatalf("events=%q err=%v", body, err)
	}
}

func TestStoreWritesSessionAndContextAtomically(t *testing.T) {
	root := t.TempDir()
	store := Open(root)
	if _, err := store.Create(context.Background(), ports.ImplementationSession{ID: "run-1", Status: ports.SessionCreated}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(context.Background(), "run-1", func(session *ports.ImplementationSession) error {
		session.Context.Summary = "edited README"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(root, "run-1", ".*"))
	for _, path := range paths {
		if !strings.HasSuffix(path, ".lock") {
			t.Fatalf("temporary file=%s", path)
		}
	}
	if err != nil {
		t.Fatalf("temporary files=%v err=%v", paths, err)
	}
	for _, name := range []string{"session.json", "context.json"} {
		info, err := os.Stat(filepath.Join(root, "run-1", name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%v err=%v", name, info.Mode(), err)
		}
	}
}
