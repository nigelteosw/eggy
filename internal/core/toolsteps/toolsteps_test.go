package toolsteps

import (
	"encoding/json"
	"strings"
	"testing"
)

// The status line is read by an owner waiting on an answer. A tool's name is
// the wrong thing to show them; what it is doing is the right thing.
func TestLabelSaysWhatAToolIsDoing(t *testing.T) {
	cases := map[string]string{
		"web_search":      "Searching the web",
		"current_time":    "Checking the time",
		"google_gmail":    "Checking email",
		"google_calendar": "Checking your calendar",
		"schedule":        "Looking at your schedules",
		// The web chat matches this label to draw its image placeholder
		// (website/src/chat/images.ts), so changing it changes both.
		"generate_image": "Drawing an image",
	}
	for tool, want := range cases {
		if got := Label(tool); got != want {
			t.Errorf("Label(%q)=%q, want %q", tool, got, want)
		}
	}
}

// A tool with no plain name still reads as an action, never as a bare
// identifier, so a new tool does not show the owner a snake_case word.
func TestUnknownToolStillReadsAsAnAction(t *testing.T) {
	got := Label("brand_new_tool")
	if !strings.HasPrefix(got, "Using ") || got == "brand_new_tool" {
		t.Fatalf("unknown tool label=%q", got)
	}
}

func TestDetailShowsWhatTheToolWasAskedFor(t *testing.T) {
	cases := map[string]string{
		`{"query":"soft boiled eggs"}`:                  "soft boiled eggs",
		`{"action":"list","max_results":5,"x":{"a":1}}`: "action: list, max_results: 5",
		`{"text":"two\n  lines"}`:                       "two lines",
		`{}`:                                            "",
		``:                                              "",
		`not json`:                                      "",
		`{"flag":true,"none":null}`:                     "flag: true",
	}
	for args, want := range cases {
		if got := Detail(json.RawMessage(args)); got != want {
			t.Errorf("Detail(%s)=%q, want %q", args, got, want)
		}
	}
}

func TestDetailIsOneShortLine(t *testing.T) {
	got := Detail(json.RawMessage(`{"query":"` + strings.Repeat("egg ", 100) + `"}`))
	if n := len([]rune(got)); n > maxDetail {
		t.Fatalf("detail has %d runes, want at most %d", n, maxDetail)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated detail %q does not say so", got)
	}
}
