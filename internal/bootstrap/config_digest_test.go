package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// The restart bar is right only if the digest moves exactly when the file does:
// an edit that is not seen would hide a pending restart, and one that is seen
// without an edit would nag.
func TestConfigDigestMovesWhenTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := configDigest(path)
	if configDigest(path) != first {
		t.Fatal("an unchanged file digests differently")
	}
	if err := os.WriteFile(path, []byte("a: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if configDigest(path) == first {
		t.Fatal("an edited file digests the same")
	}
	if configDigest(filepath.Join(t.TempDir(), "absent.yaml")) != "" {
		t.Fatal("a missing file should digest to the empty string")
	}
}
