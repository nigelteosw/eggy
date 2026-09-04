package ports

import "testing"

// The daemon and the panel must agree on what counts as an entry, or the card
// reports a heartbeat that is armed while every beat skips. They agree by
// calling this, so the cases live with the rule rather than beside one caller.
func TestWatchListIsEmpty(t *testing.T) {
	for name, tt := range map[string]struct {
		content string
		want    bool
	}{
		"empty":              {content: "", want: true},
		"blank lines":        {content: "\n   \n", want: true},
		"heading only":       {content: "# Eggy Watch\n", want: true},
		"heading and blanks": {content: "# Eggy Watch\n\n  \n", want: true},
		"one entry":          {content: "# Eggy Watch\n\n- PR #18\n", want: false},
		"entry without head": {content: "PR #18", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := WatchListIsEmpty(tt.content); got != tt.want {
				t.Fatalf("WatchListIsEmpty(%q)=%v, want %v", tt.content, got, tt.want)
			}
		})
	}
}
