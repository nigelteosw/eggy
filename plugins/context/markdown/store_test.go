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

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	return InDir(dir, DefaultUserMaxBytes, DefaultMemoryMaxBytes), dir
}

func TestContextStoreCreatesPreservesAndEditsDocuments(t *testing.T) {
	store, dir := testStore(t)
	ctx := context.Background()
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loaded.Soul, "# Eggy Soul") || !strings.HasPrefix(loaded.User, "# Eggy User") || !strings.HasPrefix(loaded.Memory, "# Eggy Memory") {
		t.Fatalf("context=%#v", loaded)
	}
	if loaded.UserMaxBytes != DefaultUserMaxBytes || loaded.MemoryMaxBytes != DefaultMemoryMaxBytes {
		t.Fatalf("budgets user=%d memory=%d", loaded.UserMaxBytes, loaded.MemoryMaxBytes)
	}
	for _, name := range []string{"SOUL.md", "USER.md", "MEMORY.md"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%v err=%v", name, info.Mode().Perm(), err)
		}
	}
	if err := store.AddEntry(ctx, ports.ContextUser, "Prefers concise answers"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEntry(ctx, ports.ContextMemory, "Eggy is trusted"); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.User, "- Prefers concise answers") || !strings.Contains(loaded.Memory, "- Eggy is trusted") {
		t.Fatalf("context=%#v", loaded)
	}
	// The document's own title survives editing, so the file stays readable.
	if !strings.HasPrefix(loaded.Memory, "# Eggy Memory\n") {
		t.Fatalf("header lost: %q", loaded.Memory)
	}
}

func TestContextStoreReplacesAndRemovesEntriesBySubstring(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	for _, text := range []string{"alpha fact", "beta fact", "gamma fact"} {
		if err := store.AddEntry(ctx, ports.ContextMemory, text); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ReplaceEntry(ctx, ports.ContextMemory, "beta", "beta fact, corrected"); err != nil {
		t.Fatal(err)
	}
	// Removing the middle entry must leave its neighbours untouched and no
	// blank-line artifact at the splice point.
	if err := store.RemoveEntry(ctx, ports.ContextMemory, "beta"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loaded.Memory, "beta") {
		t.Fatalf("removed entry still present: %q", loaded.Memory)
	}
	if !strings.Contains(loaded.Memory, "- alpha fact\n- gamma fact\n") {
		t.Fatalf("unexpected splice: %q", loaded.Memory)
	}
	// Emptying the document returns it to its initial state, not to a file of
	// dangling blank lines.
	if err := store.RemoveEntry(ctx, ports.ContextMemory, "alpha"); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveEntry(ctx, ports.ContextMemory, "gamma"); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Memory != initialMemory {
		t.Fatalf("expected document reset to initial state, got %q", loaded.Memory)
	}
}

// TestContextStoreRejectsMissingAndAmbiguousMatches proves old_text must
// identify exactly one entry: a miss and an ambiguous match both fail loudly
// rather than editing nothing or guessing at the first match.
func TestContextStoreRejectsMissingAndAmbiguousMatches(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	if err := store.RemoveEntry(ctx, ports.ContextMemory, "absent"); err == nil {
		t.Fatal("expected error removing an entry that does not exist")
	}
	for _, text := range []string{"shared prefix one", "shared prefix two"} {
		if err := store.AddEntry(ctx, ports.ContextMemory, text); err != nil {
			t.Fatal(err)
		}
	}
	err := store.RemoveEntry(ctx, ports.ContextMemory, "shared prefix")
	if err == nil || !strings.Contains(err.Error(), "2 entries") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	if err := store.ReplaceEntry(ctx, ports.ContextMemory, "shared prefix", "merged"); err == nil {
		t.Fatal("expected ambiguity error on replace")
	}
	// A longer old_text disambiguates.
	if err := store.RemoveEntry(ctx, ports.ContextMemory, "shared prefix one"); err != nil {
		t.Fatal(err)
	}
}

// TestContextStoreReadsDocumentsWrittenByTheSectionedStore proves the
// migration path: a MEMORY.md left behind by the older section-based store
// keeps loading, its headings survive as decoration, and its body lines are
// ordinary matchable entries.
func TestContextStoreReadsDocumentsWrittenByTheSectionedStore(t *testing.T) {
	store, dir := testStore(t)
	ctx := context.Background()
	legacy := "# Eggy Memory\n\n## Repositories\n\nEggy is trusted\n\n## Preferences\n\nShip small changes\n"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx)
	if err != nil || loaded.Memory != legacy {
		t.Fatalf("memory=%q err=%v", loaded.Memory, err)
	}
	if err := store.ReplaceEntry(ctx, ports.ContextMemory, "Eggy is trusted", "Eggy is trusted for eggy"); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded.Memory, "- Eggy is trusted for eggy") {
		t.Fatalf("legacy entry not editable: %q", loaded.Memory)
	}
	if !strings.Contains(loaded.Memory, "Ship small changes") {
		t.Fatalf("legacy sibling lost: %q", loaded.Memory)
	}
}

// TestContextStoreBudgetIsEnforcedOnWriteNotLoad proves an over-budget
// document still loads -- so a file predating the budget is never
// unreadable -- while the write that would grow it further is refused.
func TestContextStoreBudgetIsEnforcedOnWriteNotLoad(t *testing.T) {
	dir := t.TempDir()
	store := InDir(dir, DefaultUserMaxBytes, 64)
	ctx := context.Background()
	oversized := "# Eggy Memory\n\n- " + strings.Repeat("x", 200) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx)
	if err != nil || loaded.Memory != oversized {
		t.Fatalf("memory=%q err=%v", loaded.Memory, err)
	}
	err = store.AddEntry(ctx, ports.ContextMemory, "one more fact")
	if err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("expected budget error, got %v", err)
	}
	// Removal must still work when full, or the agent could never recover.
	if err := store.RemoveEntry(ctx, ports.ContextMemory, "xxxx"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEntry(ctx, ports.ContextMemory, "one more fact"); err != nil {
		t.Fatal(err)
	}
}

func TestContextStoreSoulIsOwnerEditableOnly(t *testing.T) {
	store, dir := testStore(t)
	ctx := context.Background()
	if err := store.AddEntry(ctx, ports.ContextSoul, "check something"); err == nil {
		t.Fatal("expected soul to reject an add")
	}
	if _, err := store.Load(ctx); err != nil {
		t.Fatal(err)
	}
	soul := "# Eggy Soul\n\nCustom identity the owner wrote.\n"
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte(soul), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx)
	if err != nil || loaded.Soul != soul {
		t.Fatalf("soul=%q err=%v", loaded.Soul, err)
	}
}

// TestContextStoreFlattensEntriesToOneLine proves no write can reshape the
// document: newlines collapse and a leading "#" cannot forge a heading.
func TestContextStoreFlattensEntriesToOneLine(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	if err := store.AddEntry(ctx, ports.ContextMemory, "## Injected\n\nbody line"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loaded.Memory, "\n## Injected") {
		t.Fatalf("entry forged a heading: %q", loaded.Memory)
	}
	if !strings.Contains(loaded.Memory, "- Injected body line") {
		t.Fatalf("entry not flattened: %q", loaded.Memory)
	}
	if err := store.AddEntry(ctx, ports.ContextMemory, "   \n  "); err == nil {
		t.Fatal("expected empty entry to be rejected")
	}
}

func TestContextStoreSerializesConcurrentWrites(t *testing.T) {
	store, _ := testStore(t)
	var workers sync.WaitGroup
	errorsChannel := make(chan error, 8)
	for i := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsChannel <- store.AddEntry(context.Background(), ports.ContextMemory, "fact "+string(rune('a'+i)))
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
		if !strings.Contains(loaded.Memory, "fact "+string(rune('a'+i))) {
			t.Fatalf("missing write %d in %q", i, loaded.Memory)
		}
	}
}

// A document written by the older section-based store is flattened on its
// first edit: body lines survive as entries, "## Section" headings below the
// first entry do not. See splitEntries.
func TestContextStoreFlattensLegacySectionedDocument(t *testing.T) {
	store, dir := testStore(t)
	path := filepath.Join(dir, "MEMORY.md")
	legacy := "# Eggy Memory\n\n## Deploy\n\n- ship via railway\n\n## Conventions\n\n- tabs not spaces\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEntry(context.Background(), ports.ContextMemory, "new fact"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(data)
	for _, entry := range []string{"- ship via railway", "- tabs not spaces", "- new fact"} {
		if !strings.Contains(updated, entry) {
			t.Fatalf("entry %q lost: %q", entry, updated)
		}
	}
	if !strings.HasPrefix(updated, "# Eggy Memory") {
		t.Fatalf("title lost: %q", updated)
	}
	if strings.Contains(updated, "## Conventions") {
		t.Fatalf("heading below first entry should be flattened away: %q", updated)
	}
}

// A document already over budget must still accept edits that shrink it,
// otherwise the only edits that could recover it are the ones rejected.
func TestContextStoreOverBudgetDocumentAcceptsShrinkingEdits(t *testing.T) {
	store, dir := testStore(t)
	path := filepath.Join(dir, "MEMORY.md")
	oversized := "# Eggy Memory\n\n"
	for i := 0; i < 200; i++ {
		oversized += "- fact " + string(rune('a'+i%26)) + strings.Repeat("x", 40) + "\n"
	}
	oversized += "- uniquely removable fact\n"
	if int64(len(oversized)) <= DefaultMemoryMaxBytes {
		t.Fatalf("fixture is not over budget: %d bytes", len(oversized))
	}
	if err := os.WriteFile(path, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.RemoveEntry(ctx, ports.ContextMemory, "uniquely removable fact"); err != nil {
		t.Fatalf("shrinking edit rejected: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "uniquely removable fact") {
		t.Fatalf("entry not removed: %q", string(data))
	}
	if len(data) >= len(oversized) {
		t.Fatalf("document did not shrink: %d -> %d", len(oversized), len(data))
	}
	// Growing it further is still refused while it stays over budget.
	if err := store.AddEntry(ctx, ports.ContextMemory, "a brand new fact"); err == nil {
		t.Fatal("growing an over-budget document should fail")
	}
}

func TestWatchDocumentRoundTrips(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()

	if err := store.ReplaceDocument(ctx, ports.ContextWatch, "# Eggy Watch\n\nPR #18 open since Aug 20\n"); err != nil {
		t.Fatalf("ReplaceDocument: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(loaded.Watch, "PR #18 open since Aug 20") {
		t.Fatalf("watch=%q", loaded.Watch)
	}
	if loaded.WatchMaxBytes != DefaultWatchMaxBytes {
		t.Fatalf("WatchMaxBytes=%d want %d", loaded.WatchMaxBytes, DefaultWatchMaxBytes)
	}
}

func TestWatchDocumentAcceptsEntryEdits(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()

	if err := store.AddEntry(ctx, ports.ContextWatch, "PR #18 open since Aug 20"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if err := store.ReplaceEntry(ctx, ports.ContextWatch, "PR #18", "PR #18 open since Aug 20 — mentioned Aug 22"); err != nil {
		t.Fatalf("ReplaceEntry: %v", err)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(loaded.Watch, "mentioned Aug 22") {
		t.Fatalf("watch=%q", loaded.Watch)
	}
}

// Soul stays load-only through every write path, ReplaceDocument included.
func TestReplaceDocumentRefusesSoul(t *testing.T) {
	store, _ := testStore(t)
	err := store.ReplaceDocument(context.Background(), ports.ContextSoul, "rewritten")
	if err == nil {
		t.Fatal("ReplaceDocument on soul succeeded")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err=%v", err)
	}
}

func TestReplaceDocumentRefusesAnOverBudgetWrite(t *testing.T) {
	store, _ := testStore(t)
	oversized := "# Eggy Watch\n\n" + strings.Repeat("x", int(DefaultWatchMaxBytes)+1)
	err := store.ReplaceDocument(context.Background(), ports.ContextWatch, oversized)
	if err == nil {
		t.Fatal("oversized ReplaceDocument succeeded")
	}
	if !strings.Contains(err.Error(), "is full") {
		t.Fatalf("err=%v", err)
	}
}

// A rejected write must leave the previous content intact: the beat that
// wrote it still has to deliver its notification against a sane document.
func TestReplaceDocumentLeavesContentIntactOnRejection(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	if err := store.ReplaceDocument(ctx, ports.ContextWatch, "# Eggy Watch\n\nkeep me\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	oversized := "# Eggy Watch\n\n" + strings.Repeat("x", int(DefaultWatchMaxBytes)+1)
	if err := store.ReplaceDocument(ctx, ports.ContextWatch, oversized); err == nil {
		t.Fatal("oversized write succeeded")
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(loaded.Watch, "keep me") {
		t.Fatalf("watch=%q", loaded.Watch)
	}
}
