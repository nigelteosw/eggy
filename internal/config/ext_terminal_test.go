package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each account's terminal directory is named with a hash keyed by the
// encryption key, so enabling the terminal without one is refused at load,
// where /restart's pre-flight sees it, rather than at boot.
func TestEnabledTerminalRequiresEncryptionKey(t *testing.T) {
	body := validConfig() + "\nterminal:\n  enabled: true\n"
	env := testSecrets()
	delete(env, "EGGY_ENCRYPTION_KEY")
	if _, _, err := loadText(t, body, env); err == nil || !strings.Contains(err.Error(), "EGGY_ENCRYPTION_KEY") {
		t.Fatalf("error=%v", err)
	}
	cfg, _, err := loadText(t, body, testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Terminal.Enabled {
		t.Fatal("terminal.enabled did not load")
	}
}

func TestSetTerminalEnabledRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTerminalEnabled(path, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Terminal.Enabled {
		t.Fatal("not saved")
	}
}
