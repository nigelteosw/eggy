package bootstrap

import "testing"

func TestProgressJoinsDistinctLabels(t *testing.T) {
	if got := progressLine([]string{"web_search", "web_search", "current_time"}); got != "Searching the web, Checking the time…" {
		t.Fatalf("progressLine=%q", got)
	}
}
