package markdown

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

func as(id string) context.Context {
	return ports.WithPrincipal(context.Background(), ports.Principal{AccountID: id})
}

// accountStore is a store laid out the way bootstrap lays one out: one SOUL
// at the top of the home and one memories directory per account.
func accountStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store := Open(Paths{
		Soul: filepath.Join(dir, "SOUL.md"),
		Memories: func(accountID string) (string, error) {
			if strings.ContainsAny(accountID, "/\\") || accountID == "" || accountID == ".." {
				return "", errors.New("invalid account id")
			}
			return filepath.Join(dir, "accounts", accountID, "memories"), nil
		},
	}, DefaultUserMaxBytes, DefaultMemoryMaxBytes, DefaultWatchMaxBytes)
	return store, dir
}

func TestPrivateDocumentsAreScopedByAccount(t *testing.T) {
	store, dir := accountStore(t)
	if err := store.AddEntry(as("a"), ports.ContextMemory, "a's secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEntry(as("a"), ports.ContextUser, "a likes tea"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDocument(as("a"), ports.ContextWatch, "# Eggy Watch\n\n- the oven\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDocument(as("a"), ports.ContextSoul, "# Shared soul\n"); err != nil {
		t.Fatal(err)
	}
	b, err := store.Load(as("b"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.Memory, "secret") || strings.Contains(b.User, "tea") || strings.Contains(b.Watch, "oven") {
		t.Fatalf("b sees a's documents: %#v", b)
	}
	a, err := store.Load(as("a"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Memory, "a's secret") {
		t.Fatalf("a lost its memory: %#v", a)
	}
	// SOUL is one file, shared.
	if a.Soul != b.Soul || a.Soul != "# Shared soul\n" {
		t.Fatal("soul differs between accounts")
	}
	if _, err := os.Stat(filepath.Join(dir, "SOUL.md")); err != nil {
		t.Fatalf("shared soul not at the home root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "accounts", "a", "memories", "MEMORY.md")); err != nil {
		t.Fatalf("a's memory not under its account: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memories")); !os.IsNotExist(err) {
		t.Fatal("a shared memories directory was written")
	}
}

func TestPrivateDocumentsRequireAPrincipal(t *testing.T) {
	store, _ := accountStore(t)
	ctx := context.Background()
	if _, err := store.Load(ctx); !errors.Is(err, ports.ErrNoPrincipal) {
		t.Fatalf("Load err=%v", err)
	}
	if err := store.AddEntry(ctx, ports.ContextMemory, "x"); !errors.Is(err, ports.ErrNoPrincipal) {
		t.Fatalf("AddEntry err=%v", err)
	}
	if err := store.ReplaceDocument(ctx, ports.ContextWatch, "x"); !errors.Is(err, ports.ErrNoPrincipal) {
		t.Fatalf("ReplaceDocument err=%v", err)
	}
}

func TestAnInvalidAccountIDCannotRedirectAWrite(t *testing.T) {
	store, dir := accountStore(t)
	if err := store.AddEntry(as("../escape"), ports.ContextMemory, "x"); err == nil {
		t.Fatal("path-escaping account id accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatal("write escaped the accounts directory")
	}
}

func TestASymlinkedAccountDirectoryIsRefused(t *testing.T) {
	store, dir := accountStore(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "accounts", "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "accounts", "a", "memories")); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEntry(as("a"), ports.ContextMemory, "x"); err == nil {
		t.Fatal("write through a symlinked memories directory accepted")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("write landed outside the home: %v", entries)
	}
}
