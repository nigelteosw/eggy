package bootstrap

import (
	"os"
	"testing"
)

// TestReadmeReferencesExistingFiles guards against README.md pointing at
// setup/deployment files that were renamed or removed.
func TestReadmeReferencesExistingFiles(t *testing.T) {
	for _, path := range []string{
		"../../config.example.yaml",
		"../../.env.example",
		"../../railway.toml",
		"../../Makefile",
		"../../Dockerfile",
		"../../AGENTS.md",
		"../../docs/ARCHITECTURE.md",
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("README.md references %q, but it does not exist: %v", path, err)
		}
	}
}
