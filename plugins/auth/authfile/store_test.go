package authfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return Open(filepath.Join(t.TempDir(), "auth.json"))
}

func TestReadReportsMissingRecords(t *testing.T) {
	store := newStore(t)
	if _, err := store.Read("mcp", "railway"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

func TestWriteAndReadRoundTripsAndIsOwnerOnly(t *testing.T) {
	store := newStore(t)
	if err := store.Write("mcp", "railway", json.RawMessage(`{"ciphertext":"abc"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read("mcp", "railway")
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct{ Ciphertext string }
	if err := json.Unmarshal(got, &decoded); err != nil || decoded.Ciphertext != "abc" {
		t.Fatalf("record=%s err=%v", got, err)
	}
	info, err := os.Stat(store.Path())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
}

// TestSectionsDoNotClobberEachOther proves the shared document is safe for
// two adapters: writing one section's record must not disturb a credential
// another adapter is holding in the same file.
func TestSectionsDoNotClobberEachOther(t *testing.T) {
	store := newStore(t)
	if err := store.Write("tokens", "github", json.RawMessage(`{"ciphertext":"sealed"}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("mcp", "railway", json.RawMessage(`{"ciphertext":"abc"}`)); err != nil {
		t.Fatal(err)
	}
	body, err := store.Read("tokens", "github")
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Ciphertext string `json:"ciphertext"`
	}
	if err := json.Unmarshal(body, &record); err != nil || record.Ciphertext != "sealed" {
		t.Fatalf("tokens=%s err=%v", body, err)
	}
	if _, err := store.Read("mcp", "railway"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRemovesTheRecordAndEmptySection(t *testing.T) {
	store := newStore(t)
	if err := store.Write("mcp", "railway", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("mcp", "railway"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("mcp", "railway"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
	// Deleting something already gone is not an error: callers should not
	// have to check first.
	if err := store.Delete("mcp", "railway"); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidNamesAreRejected(t *testing.T) {
	store := newStore(t)
	for _, name := range []string{"", "../escape", "with/slash", "with space"} {
		if err := store.Write(name, "key", json.RawMessage(`{}`)); err == nil {
			t.Fatalf("section %q was accepted", name)
		}
		if err := store.Write("mcp", name, json.RawMessage(`{}`)); err == nil {
			t.Fatalf("key %q was accepted", name)
		}
	}
}

func TestUnsupportedVersionIsRefused(t *testing.T) {
	store := newStore(t)
	if err := os.WriteFile(store.Path(), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read("mcp", "railway"); err == nil {
		t.Fatal("expected an unsupported auth.json version to be refused")
	}
}
