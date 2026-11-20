package bootstrap

import (
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/home"
)

func TestTerminalOffCostsNothing(t *testing.T) {
	layout := home.Layout{Root: t.TempDir()}
	svc, err := newTerminal(config.Config{}, config.Secrets{EncryptionKey: "k"}, layout, sandboxState{ok: true}, nil)
	if err != nil || svc != nil {
		t.Fatalf("disabled terminal = %v, %v", svc, err)
	}
	if box := setupSandbox(config.Config{}, layout); box.locked {
		t.Fatal("the data directory was locked with no extension using the sandbox")
	}
}

// A shell is only offered where the confinement was proved.
func TestTerminalNeedsAPassingProbe(t *testing.T) {
	cfg := config.Config{Terminal: config.TerminalConfig{Enabled: true}}
	svc, err := newTerminal(cfg, config.Secrets{EncryptionKey: "k"}, home.Layout{Root: t.TempDir()}, sandboxState{locked: true, reason: "probe failed"}, nil)
	if err != nil || svc != nil {
		t.Fatalf("terminal without a passing probe = %v, %v", svc, err)
	}
}

func TestTerminalDirNamesAreKeyedAndStable(t *testing.T) {
	if _, err := accountDirName(""); err == nil {
		t.Fatal("named directories without a key")
	}
	name, err := accountDirName("key-one")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := accountDirName("key-two")
	alice := name("alice")
	if alice != name("alice") {
		t.Fatal("the name changed between calls")
	}
	if alice == name("bob") {
		t.Fatal("two accounts share a directory")
	}
	if alice == other("alice") {
		t.Fatal("the name does not depend on the key")
	}
	if strings.Contains(alice, "alice") || len(alice) != 32 || strings.ContainsAny(alice, "/.") {
		t.Fatalf("name %q reveals the account or is not a plain directory name", alice)
	}
}
