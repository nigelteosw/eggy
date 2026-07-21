package bootstrap

import (
	"os"
	"regexp"
	"testing"
)

// commandsWithoutReadmeEntry lists CommandService switch cases that are
// deliberately not documented in README.md's operator-facing command list:
// /start and /help are Telegram/bot-framework conventions rather than
// operational shortcuts, and /prompts is pending the product decision
// tracked in TODO.md ("Decide whether custom prompts earn their
// complexity"). Remove an entry here only when README.md documents it.
var commandsWithoutReadmeEntry = map[string]bool{
	"/start":   true,
	"/help":    true,
	"/prompts": true,
}

// TestReadmeDocumentsImplementedCommands guards against README.md drifting
// from CommandService's actual command set (TODO.md: "validate command
// names ... against the shared command catalog or current source").
func TestReadmeDocumentsImplementedCommands(t *testing.T) {
	commandsSrc, err := os.ReadFile("commands.go")
	if err != nil {
		t.Fatalf("read commands.go: %v", err)
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	caseRE := regexp.MustCompile(`case "(/[a-z_]+)":`)
	matches := caseRE.FindAllStringSubmatch(string(commandsSrc), -1)
	if len(matches) == 0 {
		t.Fatal("no `case \"/...\":` commands found in commands.go; regex likely stale")
	}

	for _, m := range matches {
		name := m[1]
		if commandsWithoutReadmeEntry[name] {
			continue
		}
		if !regexp.MustCompile(regexp.QuoteMeta("`" + name)).Match(readme) {
			t.Errorf("commands.go implements %q but README.md does not document it; update README.md or add it to commandsWithoutReadmeEntry with a reason", name)
		}
	}
}

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
