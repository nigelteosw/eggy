package services

import "testing"

func TestDescribeCronSaysCommonSchedulesInWords(t *testing.T) {
	cases := map[string]string{
		"0 9 * * 1-5":    "Every weekday at 9:00",
		"30 8 * * *":     "Every day at 8:30",
		"0 9,17 * * *":   "Every day at 9:00 and 17:00",
		"0 10 * * 0,6":   "Every weekend at 10:00",
		"0 18 * * 5":     "Every Friday at 18:00",
		"15 7 * * 1,3,5": "Every Monday, Wednesday and Friday at 7:15",
		"0 8 1 * *":      "On the 1st of each month at 8:00",
		"0 8 22 * *":     "On the 22nd of each month at 8:00",
		"0 * * * *":      "Every hour",
		"*/15 * * * *":   "Every 15 minutes",
		"0 */3 * * *":    "Every 3 hours",
	}
	for expression, want := range cases {
		if got := DescribeCron(expression); got != want {
			t.Errorf("DescribeCron(%q) = %q, want %q", expression, got, want)
		}
	}
}

// A shape the describer does not know is shown as the expression itself, never
// guessed at.
func TestDescribeCronFallsBackToTheExpression(t *testing.T) {
	for _, expression := range []string{"0 9 1-7 * 1", "0 9 * 6 *", "5-10 9 * * *", "nonsense"} {
		if got := DescribeCron(expression); got != "On the schedule `"+expression+"`" {
			t.Errorf("DescribeCron(%q) = %q", expression, got)
		}
	}
}
