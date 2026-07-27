package commands

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
// It reads the catalog itself rather than scanning commands.go for `Path:`
// literals, so it cannot be quietly defeated by moving or reformatting the
// registration code.
func TestReadmeDocumentsCatalogCommands(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	entries := Catalog()
	if len(entries) == 0 {
		t.Fatal("catalog is empty")
	}

	topLevel := map[string]bool{}
	for _, entry := range entries {
		topLevel[strings.Fields(entry.Path)[0]] = true
	}

	for name := range topLevel {
		if topLevelCommandsWithoutReadmeEntry[name] {
			continue
		}
		if !regexp.MustCompile(regexp.QuoteMeta("/" + name)).Match(readme) {
			t.Errorf("the catalog registers top-level command %q but README.md does not document /%s; update README.md or add it to topLevelCommandsWithoutReadmeEntry with a reason", name, name)
		}
	}
}
