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
	if !strings.HasPrefix(loaded.Soul, "# Eggy Soul") || !strings.HasPrefix(loaded.User, "# Eggy User") || !strings.HasPrefix(loaded.Memory, "# Eggy Memory") || !strings.HasPrefix(loaded.Heartbeat, "# Eggy Heartbeat") {
		t.Fatalf("context=%#v", loaded)
	}
	if loaded.MaxBytes != 64<<10 {
		t.Fatalf("MaxBytes=%d", loaded.MaxBytes)
	}
	for _, name := range []string{"SOUL.md", "USER.md", "MEMORY.md", "HEARTBEAT.md"} {
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

func TestContextStoreRemoveSectionSplicesCleanlyAndErrorsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir, 64<<10)
	ctx := context.Background()
	if err := store.Append(ctx, ports.ContextMemory, "First", "one"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, ports.ContextMemory, "Second", "two"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, ports.ContextMemory, "Third", "three"); err != nil {
		t.Fatal(err)
	}
	// Remove the middle section and confirm its neighbors survive untouched,
	// with no leftover blank-line artifacts at the splice point.
	if err := store.RemoveSection(ctx, ports.ContextMemory, "Second"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loaded.Memory, "Second") || strings.Contains(loaded.Memory, "two") {
		t.Fatalf("removed section still present: %q", loaded.Memory)
	}
	if !strings.Contains(loaded.Memory, "## First\n\none\n\n## Third\n\nthree\n") {
		t.Fatalf("unexpected splice: %q", loaded.Memory)
	}
	// Removing the last remaining section should leave a clean document, not
	// dangling blank lines.
	if err := store.RemoveSection(ctx, ports.ContextMemory, "First"); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveSection(ctx, ports.ContextMemory, "Third"); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Memory != initialMemory {
		t.Fatalf("expected document reset to initial state, got %q", loaded.Memory)
	}
	if err := store.RemoveSection(ctx, ports.ContextMemory, "Missing"); err == nil {
		t.Fatal("expected error removing a section that does not exist")
	}
}

func TestContextStoreEditsSoulAndRejectsOversizedFiles(t *testing.T) {
	store := Open(t.TempDir(), 64<<10)
	if err := store.Append(context.Background(), ports.ContextSoul, "Identity", "changed"); err != nil {
		t.Fatalf("expected SOUL edit to succeed, got %v", err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil || !strings.Contains(loaded.Soul, "## Identity\n\nchanged") {
		t.Fatalf("context=%#v err=%v", loaded, err)
	}

	dir := t.TempDir()
	small := Open(dir, 16)
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(strings.Repeat("x", 17)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := small.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error=%v", err)
	}
}

// TestContextStoreHeartbeatIsHumanEditableOnly proves HEARTBEAT.md loads
// like the other durable documents but has no agent-writable path: it is a
// human-editable checklist file, not a fourth document the agent tools can
// append, replace, or remove sections on.
func TestContextStoreHeartbeatIsHumanEditableOnly(t *testing.T) {
	store := Open(t.TempDir(), 64<<10)
	ctx := context.Background()
	if err := store.Append(ctx, ports.ContextHeartbeat, "Extra", "check something"); err == nil {
		t.Fatal("expected HEARTBEAT.md to reject an agent-tool-shaped write")
	}
	if err := store.ReplaceSection(ctx, ports.ContextHeartbeat, "Extra", "check something"); err == nil {
		t.Fatal("expected HEARTBEAT.md to reject an agent-tool-shaped replace")
	}
	if err := store.RemoveSection(ctx, ports.ContextHeartbeat, "Extra"); err == nil {
		t.Fatal("expected HEARTBEAT.md to reject an agent-tool-shaped remove")
	}
	dir := t.TempDir()
	editable := Open(dir, 64<<10)
	if _, err := editable.Load(ctx); err != nil {
		t.Fatal(err)
	}
	custom := "# Eggy Heartbeat\n\n## Check\n\nSomething the owner cares about.\n"
	if err := os.WriteFile(filepath.Join(dir, "HEARTBEAT.md"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := editable.Load(ctx)
	if err != nil || loaded.Heartbeat != custom {
		t.Fatalf("heartbeat=%q err=%v", loaded.Heartbeat, err)
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
