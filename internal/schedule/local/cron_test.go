package local

import (
	"testing"
	"time"
)

func TestCronNextSupportsWildcardsStepsRangesAndLists(t *testing.T) {
	location := time.FixedZone("SGT", 8*60*60)
	tests := []struct {
		expression  string
		after, want time.Time
	}{
		{"*/15 * * * *", time.Date(2026, 7, 19, 10, 7, 0, 0, location), time.Date(2026, 7, 19, 10, 15, 0, 0, location)},
		{"0 9 * * 1-5", time.Date(2026, 7, 17, 9, 1, 0, 0, location), time.Date(2026, 7, 20, 9, 0, 0, 0, location)},
		{"30 8 1,15 * *", time.Date(2026, 7, 2, 0, 0, 0, 0, location), time.Date(2026, 7, 15, 8, 30, 0, 0, location)},
	}
	for _, tc := range tests {
		cron, err := ParseCron(tc.expression)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", tc.expression, err)
		}
		got, err := cron.Next(tc.after)
		if err != nil || !got.Equal(tc.want) {
			t.Fatalf("%s Next()=%v, %v want %v", tc.expression, got, err, tc.want)
		}
	}
}

func TestCronRejectsInvalidExpressions(t *testing.T) {
	for _, expression := range []string{"* * *", "60 * * * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "5-2 * * * *"} {
		if _, err := ParseCron(expression); err == nil {
			t.Fatalf("ParseCron(%q) succeeded", expression)
		}
	}
}
