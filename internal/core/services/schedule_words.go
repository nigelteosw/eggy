package services

import (
	"fmt"
	"strconv"
	"strings"
)

// DescribeCron says a five-field cron expression the way a person would:
// "Every weekday at 9:00", "Every hour", "On the 1st of each month at 8:30".
// The schedule tool's replies and approval cards, and the Scheduled page, all
// read it from here, so Telegram and the panel describe one schedule in the
// same words. A shape it does not recognise comes back as the expression
// itself, quoted -- a schedule is never described wrongly to look friendlier.
func DescribeCron(expression string) string {
	fallback := "On the schedule `" + strings.TrimSpace(expression) + "`"
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return fallback
	}
	minute, hour, day, month, weekday := fields[0], fields[1], fields[2], fields[3], fields[4]
	if month != "*" {
		return fallback
	}

	// Every N minutes or hours, with no day restriction.
	if day == "*" && weekday == "*" {
		switch {
		case minute == "*" && hour == "*":
			return "Every minute"
		case strings.HasPrefix(minute, "*/") && hour == "*":
			if n, ok := cronNumber(minute[2:], 1, 59); ok {
				return "Every " + countOf(n, "minute")
			}
			return fallback
		case isCronNumber(minute, 0, 59) && hour == "*":
			if minute == "0" {
				return "Every hour"
			}
			return "Every hour at " + minute + " past"
		case isCronNumber(minute, 0, 59) && strings.HasPrefix(hour, "*/"):
			if n, ok := cronNumber(hour[2:], 1, 23); ok {
				return "Every " + countOf(n, "hour")
			}
			return fallback
		}
	}

	at, ok := cronTimes(minute, hour)
	if !ok {
		return fallback
	}
	switch {
	case day == "*" && weekday == "*":
		return "Every day at " + at
	case day == "*":
		days, ok := cronWeekdays(weekday)
		if !ok {
			return fallback
		}
		return days + " at " + at
	case weekday == "*":
		n, ok := cronNumber(day, 1, 31)
		if !ok {
			return fallback
		}
		return "On the " + ordinal(n) + " of each month at " + at
	}
	return fallback
}

// cronTimes reads a fixed minute and one or more fixed hours as clock times:
// "9:00", or "9:00 and 17:00".
func cronTimes(minute, hour string) (string, bool) {
	m, ok := cronNumber(minute, 0, 59)
	if !ok {
		return "", false
	}
	var times []string
	for _, part := range strings.Split(hour, ",") {
		h, ok := cronNumber(part, 0, 23)
		if !ok {
			return "", false
		}
		times = append(times, fmt.Sprintf("%d:%02d", h, m))
	}
	return joinWords(times), true
}

var weekdayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

func cronWeekdays(field string) (string, bool) {
	switch field {
	case "1-5", "MON-FRI", "mon-fri":
		return "Every weekday", true
	case "0,6", "6,0", "6,7", "SAT,SUN", "sat,sun":
		return "Every weekend", true
	}
	var names []string
	for _, part := range strings.Split(field, ",") {
		if from, to, isRange := strings.Cut(part, "-"); isRange {
			a, okA := cronNumber(from, 0, 7)
			b, okB := cronNumber(to, 0, 7)
			if !okA || !okB || a > b {
				return "", false
			}
			names = append(names, weekdayNames[a%7]+" to "+weekdayNames[b%7])
			continue
		}
		n, ok := cronNumber(part, 0, 7)
		if !ok {
			return "", false
		}
		names = append(names, weekdayNames[n%7])
	}
	return "Every " + joinWords(names), true
}

func cronNumber(value string, minimum, maximum int) (int, bool) {
	n, err := strconv.Atoi(value)
	if err != nil || n < minimum || n > maximum {
		return 0, false
	}
	return n, true
}

func isCronNumber(value string, minimum, maximum int) bool {
	_, ok := cronNumber(value, minimum, maximum)
	return ok
}

func countOf(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
