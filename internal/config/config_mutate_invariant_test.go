package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The nine Set* helpers used to each spell out lock, load, validate, write by
// hand. All nine held the invariant; nothing asserted it, so the tenth was
// free to drop the validate and write an invalid config the owner would only
// discover at the next restart, in safe mode. These tests pin the property on
// mutate itself, which is now the only way to write the file.

func TestMutateRefusesAnInvalidResultAndLeavesTheFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte(validConfig())
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	// A negative retention is rejected by Config.Validate, so this apply
	// produces a document that must never reach disk.
	err := mutate(path, func(cfg *Config) error {
		cfg.Tracing.Retention = Duration(-time.Second)
		return nil
	})
	if err == nil {
		t.Fatal("mutate accepted a config Validate rejects")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("refused write still changed the file:\n%s", after)
	}
}

func TestMutatePropagatesAnApplyRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte(validConfig())
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("not this time")
	// A setter refusing its own input -- SetMCPServer on a stdio server,
	// SetHeartbeat without a Telegram channel -- must leave the file alone
	// too, not just report the error.
	err := mutate(path, func(cfg *Config) error {
		cfg.Appearance.Theme = ThemeLight
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("mutate error = %v, want %v", err, refused)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("refused apply still changed the file:\n%s", after)
	}
}

func TestMutateWritesAnAcceptedChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mutate(path, func(cfg *Config) error {
		cfg.Appearance.Theme = ThemeLight
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Appearance.Theme != ThemeLight {
		t.Fatalf("theme = %q, want %q", cfg.Appearance.Theme, ThemeLight)
	}
}
