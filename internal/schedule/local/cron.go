package local

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronField struct {
	allowed  map[int]bool
	wildcard bool
}

type Cron struct {
	minute, hour, day, month, weekday cronField
}

func ParseCron(expression string) (*Cron, error) {
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return nil, errors.New("cron expression must contain five fields")
	}
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	fields := make([]cronField, 5)
	for index, part := range parts {
		field, err := parseCronField(part, limits[index][0], limits[index][1], index == 4)
		if err != nil {
			return nil, fmt.Errorf("cron field %d: %w", index+1, err)
		}
		fields[index] = field
	}
	return &Cron{minute: fields[0], hour: fields[1], day: fields[2], month: fields[3], weekday: fields[4]}, nil
}

func parseCronField(value string, minimum, maximum int, weekday bool) (cronField, error) {
	field := cronField{allowed: map[int]bool{}, wildcard: value == "*"}
	for _, segment := range strings.Split(value, ",") {
		step := 1
		base := segment
		if strings.Contains(segment, "/") {
			parts := strings.Split(segment, "/")
			if len(parts) != 2 {
				return field, errors.New("invalid step")
			}
			base = parts[0]
			parsed, err := strconv.Atoi(parts[1])
			if err != nil || parsed <= 0 {
				return field, errors.New("invalid step")
			}
			step = parsed
		}
		start, end := minimum, maximum
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			parts := strings.Split(base, "-")
			if len(parts) != 2 {
				return field, errors.New("invalid range")
			}
			var err error
			start, err = strconv.Atoi(parts[0])
			if err != nil {
				return field, errors.New("invalid range")
			}
			end, err = strconv.Atoi(parts[1])
			if err != nil {
				return field, errors.New("invalid range")
			}
		default:
			parsed, err := strconv.Atoi(base)
			if err != nil {
				return field, errors.New("invalid value")
			}
			start, end = parsed, parsed
		}
		if start < minimum || end > maximum || start > end {
			return field, errors.New("value outside allowed range")
		}
		for candidate := start; candidate <= end; candidate += step {
			if weekday && candidate == 7 {
				candidate = 0
				field.allowed[candidate] = true
				break
			}
			field.allowed[candidate] = true
		}
	}
	if len(field.allowed) == 0 {
		return field, errors.New("field selects no values")
	}
	return field, nil
}

func (c *Cron) Next(after time.Time) (time.Time, error) {
	candidate := after.Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(5, 0, 0)
	for candidate.Before(limit) {
		if c.matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, errors.New("no cron occurrence found within five years")
}

func (c *Cron) matches(value time.Time) bool {
	if !c.minute.allowed[value.Minute()] || !c.hour.allowed[value.Hour()] || !c.month.allowed[int(value.Month())] {
		return false
	}
	dayMatches := c.day.allowed[value.Day()]
	weekdayMatches := c.weekday.allowed[int(value.Weekday())]
	switch {
	case c.day.wildcard && c.weekday.wildcard:
		return true
	case c.day.wildcard:
		return weekdayMatches
	case c.weekday.wildcard:
		return dayMatches
	default:
		return dayMatches || weekdayMatches
	}
}
