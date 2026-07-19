package markdown

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func TestContextStoreCreatesPreservesAndEditsDocuments(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 64<<10)
	ctx := context.Background()
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loaded.Soul, "# Eggy Soul") || !strings.HasPrefix(loaded.User, "# Eggy User") || !strings.HasPrefix(loaded.Memory, "# Eggy Memory") {
		t.Fatalf("context=%#v", loaded)
	}
	for _, name := range []string{"SOUL.md", "USER.md", "MEMORY.md"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%v err=%v", name, info.Mode().Perm(), err)
		}
	}
	before := []byte("# Eggy Soul\n\nCustom.\n")
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, ports.ContextUser, "Preferences", "Concise"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSection(ctx, ports.ContextMemory, "Repositories", "Eggy is trusted"); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Soul != string(before) || !strings.Contains(loaded.User, "## Preferences\n\nConcise") || !strings.Contains(loaded.Memory, "## Repositories\n\nEggy is trusted") {
		t.Fatalf("context=%#v", loaded)
	}
}

func TestContextStoreRejectsSoulEditsAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 16)
	if err := store.Append(context.Background(), ports.ContextDocument("soul"), "Identity", "changed"); err == nil {
		t.Fatal("expected SOUL edit rejection")
	}
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(strings.Repeat("x", 17)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error=%v", err)
	}
}

func TestContextStoreSerializesConcurrentWrites(t *testing.T) {
	store := Open(t.TempDir(), 64<<10)
	var workers sync.WaitGroup
	errorsChannel := make(chan error, 8)
	for i := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsChannel <- store.Append(context.Background(), ports.ContextMemory, "Facts", string(rune('a'+i)))
		}()
	}
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 8 {
		if !strings.Contains(loaded.Memory, string(rune('a'+i))) {
			t.Fatalf("missing write %d in %q", i, loaded.Memory)
		}
	}
}
