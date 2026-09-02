package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetTracingSavesAndRestoresDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTracing(path, "true", "42", "24h", "2048"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tracing.KeepTurns != 42 || cfg.Tracing.Retention.Value().String() != "24h0m0s" || cfg.Tracing.MaxBodyBytes != 2048 {
		t.Fatalf("saved tracing = %+v", cfg.Tracing)
	}
	// Blank fields are the "restore defaults" the card's button sends.
	if err := SetTracing(path, "true", "", "", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadDocument(path)
	if cfg.Tracing.KeepTurns != 500 || cfg.Tracing.MaxBodyBytes != 1<<20 || !cfg.Tracing.Active() {
		t.Fatalf("restored tracing = %+v", cfg.Tracing)
	}
}

func TestSetTracingWritesOffExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(validConfig()), 0o600)
	if err := SetTracing(path, "false", "", "", ""); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "enabled: false") {
		t.Fatalf("off must be written out, not left absent:\n%s", body)
	}
	cfg, _ := LoadDocument(path)
	if cfg.Tracing.Active() {
		t.Fatal("tracing still active after being switched off")
	}
}

func TestSetTracingRefusesNonsense(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(validConfig()), 0o600)
	for name, args := range map[string][3]string{
		"keep_turns":     {"nope", "", ""},
		"retention":      {"", "forever", ""},
		"max_body_bytes": {"", "", "big"},
		"tiny body cap":  {"", "", "10"},
		"negative keep":  {"-1", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if err := SetTracing(path, "true", args[0], args[1], args[2]); err == nil {
				t.Fatal("accepted a value that cannot mean anything")
			}
		})
	}
}
