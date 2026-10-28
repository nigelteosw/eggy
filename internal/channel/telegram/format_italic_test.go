package telegram

import (
	"strings"
	"testing"
)

// Italics are for words the model meant to emphasise. An underscore inside an
// identifier is not emphasis, so tool names and snake_case survive unchanged.
func TestItalicsEmphasiseWordsButNotIdentifiers(t *testing.T) {
	got := toTelegramHTML("I will _check_ the google_gmail tool, not google_drive_files.")
	if !strings.Contains(got, "<i>check</i>") {
		t.Fatalf("emphasis lost: %q", got)
	}
	if strings.Contains(got, "<i>") && strings.Count(got, "<i>") != 1 {
		t.Fatalf("identifier was emphasised: %q", got)
	}
	if !strings.Contains(got, "google_gmail") || !strings.Contains(got, "google_drive_files") {
		t.Fatalf("identifier changed: %q", got)
	}
}

// A run of quoted lines is one quote block, so a quotation reads as one unit
// rather than as literal ">" characters.
func TestQuotedLinesBecomeOneBlock(t *testing.T) {
	got := toTelegramHTML("Sam wrote:\n> first line\n> second line\nand I replied.")
	if !strings.Contains(got, "<blockquote>first line\nsecond line</blockquote>") {
		t.Fatalf("quote block missing: %q", got)
	}
	if strings.Contains(got, "&gt; ") {
		t.Fatalf("quote marker left in the text: %q", got)
	}
}
