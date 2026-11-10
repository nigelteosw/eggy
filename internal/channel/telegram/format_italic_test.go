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

// Shown thinking arrives folded: a label and an expandable quote, escaped as
// plain text, so the reply below it is what the reader sees first.
func TestThinkingRendersAsAnExpandableQuote(t *testing.T) {
	got := toTelegramHTML("Thinking:\nFirst <check> the **inbox**.")
	want := "<b>Thinking</b>\n<blockquote expandable>First &lt;check&gt; the **inbox**.</blockquote>"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A long thought split across messages keeps every part folded.
func TestLongThinkingSplitsIntoFoldedParts(t *testing.T) {
	text := "Thinking:\n" + strings.Repeat("a line of reasoning\n", 400)
	chunks := splitMessage(text)
	if len(chunks) < 2 {
		t.Fatalf("expected a split, got %d chunk(s)", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.HasPrefix(toTelegramHTML(chunk), "<b>Thinking</b>\n<blockquote expandable>") {
			t.Fatalf("chunk %d is not folded: %q", i, chunk[:40])
		}
		if len([]rune(chunk)) > maxMessageLength {
			t.Fatalf("chunk %d is %d runes", i, len([]rune(chunk)))
		}
	}
}
