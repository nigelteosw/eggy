package commands

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// telegramDoc is the page an owner reads to learn what they can type. It is the
// only description of the command surface outside this package.
const telegramDoc = "../../docs/src/content/docs/use/telegram.md"

// documentedCommand matches a command named at the start of a table cell, which
// is how every row in that page introduces one: `| `/status` | ... |`. Prose
// references elsewhere on the page are deliberately not matched -- the tables
// are the reference, and requiring a table row is what makes an undocumented
// command detectable.
var documentedCommand = regexp.MustCompile("(?m)^\\| `/([a-z]+)")

// The docs-site spec made this a one-time manual check, so nothing stopped the
// shipped commands and their documentation from drifting apart silently. This
// is the same class of check navigation.test.ts already runs for routes: the
// advertised surface and the page describing it must name the same commands.
func TestEveryAdvertisedCommandIsDocumented(t *testing.T) {
	page, err := os.ReadFile(filepath.FromSlash(telegramDoc))
	if err != nil {
		t.Fatal(err)
	}

	documented := map[string]bool{}
	for _, match := range documentedCommand.FindAllStringSubmatch(string(page), -1) {
		documented[match[1]] = true
	}
	advertised := map[string]bool{}
	for _, command := range TelegramAutocomplete() {
		advertised[command.Name] = true
	}

	for name := range advertised {
		if !documented[name] {
			t.Errorf("/%s is advertised to Telegram but has no row in %s", name, telegramDoc)
		}
	}
	// The other direction matters as much: a command removed from the code
	// leaves a documented instruction that now answers "Unknown command."
	for name := range documented {
		if !advertised[name] {
			t.Errorf("%s documents /%s, which is not in the advertised command surface", telegramDoc, name)
		}
	}
}

// A documented command that Execute does not handle reads as removed to the
// owner, whatever the autocomplete list claims. The advertised names are the
// list; this pins that each one actually dispatches rather than falling through
// to the unknown-command reply.
func TestEveryAdvertisedCommandDispatches(t *testing.T) {
	service := New(Options{})
	for _, command := range TelegramAutocomplete() {
		output, handled, err := service.Execute(t.Context(), "/"+command.Name)
		if err != nil {
			t.Errorf("/%s: %v", command.Name, err)
			continue
		}
		if !handled || strings.HasPrefix(output, "Unknown command.") {
			t.Errorf("/%s is advertised but not handled: handled=%v output=%q", command.Name, handled, output)
		}
	}
}
