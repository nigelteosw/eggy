package bootstrap

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// topLevelCommandsWithoutReadmeEntry lists top-level catalog commands that
// are deliberately not documented in README.md's operator-facing command
// list: start and help are Telegram/bot-framework conventions rather than
// operational shortcuts. Remove an entry here only when README.md documents
// it.
var topLevelCommandsWithoutReadmeEntry = map[string]bool{
	"start": true,
	"help":  true,
}

// TestReadmeDocumentsCatalogCommands guards against README.md drifting from
// the shared command catalog's actual top-level commands (TODO.md: "validate
// command names ... against the shared command catalog or current source").
func TestReadmeDocumentsCatalogCommands(t *testing.T) {
	catalogSrc, err := os.ReadFile("commands.go")
	if err != nil {
		t.Fatalf("read commands.go: %v", err)
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	pathRE := regexp.MustCompile(`Path:\s+"([a-z_]+(?: [a-z_]+)*)"`)
	matches := pathRE.FindAllStringSubmatch(string(catalogSrc), -1)
	if len(matches) == 0 {
		t.Fatal("no `Path: \"...\"` catalog entries found in commands.go; regex likely stale")
	}

	topLevel := map[string]bool{}
	for _, m := range matches {
		topLevel[strings.Fields(m[1])[0]] = true
	}

	for name := range topLevel {
		if topLevelCommandsWithoutReadmeEntry[name] {
			continue
		}
		if !regexp.MustCompile(regexp.QuoteMeta("/" + name)).Match(readme) {
			t.Errorf("commands.go registers top-level command %q but README.md does not document /%s; update README.md or add it to topLevelCommandsWithoutReadmeEntry with a reason", name, name)
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
