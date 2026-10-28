package bootstrap

import (
	"strings"
	"testing"
)

// The status line is read by an owner waiting on an answer. A tool's name is
// the wrong thing to show them; what it is doing is the right thing.
func TestProgressSaysWhatAToolIsDoing(t *testing.T) {
	cases := map[string]string{
		"web_search":      "Searching the web",
		"current_time":    "Checking the time",
		"google_gmail":    "Checking email",
		"google_calendar": "Checking your calendar",
		"schedule":        "Looking at your schedules",
	}
	for tool, want := range cases {
		if got := toolProgressLabel(tool); got != want {
			t.Errorf("toolProgressLabel(%q)=%q, want %q", tool, got, want)
		}
	}
}

// A tool with no plain name still reads as an action, never as a bare
// identifier, so a new tool does not show the owner a snake_case word.
func TestUnknownToolStillReadsAsAnAction(t *testing.T) {
	got := toolProgressLabel("brand_new_tool")
	if !strings.HasPrefix(got, "Using ") || got == "brand_new_tool" {
		t.Fatalf("unknown tool label=%q", got)
	}
}

func TestProgressJoinsDistinctLabels(t *testing.T) {
	if got := progressLine([]string{"web_search", "web_search", "current_time"}); got != "Searching the web, Checking the time…" {
		t.Fatalf("progressLine=%q", got)
	}
}
