package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

type HeartbeatPolicy struct {
	quietStart int
	quietEnd   int
	location   *time.Location
	minimum    time.Duration
	weekly     int
}

func NewHeartbeatPolicy(start, end string, location *time.Location, minimum time.Duration, weekly int) (*HeartbeatPolicy, error) {
	quietStart, err := parseClock(start)
	if err != nil {
		return nil, fmt.Errorf("quiet start: %w", err)
	}
	quietEnd, err := parseClock(end)
	if err != nil {
		return nil, fmt.Errorf("quiet end: %w", err)
	}
	if location == nil {
		return nil, errors.New("heartbeat timezone is required")
	}
	if minimum < 0 || weekly < 0 {
		return nil, errors.New("heartbeat limits cannot be negative")
	}
	return &HeartbeatPolicy{quietStart: quietStart, quietEnd: quietEnd, location: location, minimum: minimum, weekly: weekly}, nil
}

func parseClock(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, errors.New("time must be HH:MM")
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, errors.New("time must be HH:MM")
	}
	return hour*60 + minute, nil
}

func (p *HeartbeatPolicy) CanSend(state ports.State, now time.Time) bool {
	local := now.In(p.location)
	minute := local.Hour()*60 + local.Minute()
	quiet := false
	if p.quietStart < p.quietEnd {
		quiet = minute >= p.quietStart && minute < p.quietEnd
	} else if p.quietStart > p.quietEnd {
		quiet = minute >= p.quietStart || minute < p.quietEnd
	}
	if quiet {
		return false
	}
	cutoff := now.Add(-7 * 24 * time.Hour)
	recent := make([]time.Time, 0, len(state.ProactiveMessages))
	for _, sent := range state.ProactiveMessages {
		if sent.After(cutoff) {
			recent = append(recent, sent)
		}
	}
	if p.weekly > 0 && len(recent) >= p.weekly {
		return false
	}
	if len(recent) > 0 && p.minimum > 0 {
		latest := recent[0]
		for _, sent := range recent[1:] {
			if sent.After(latest) {
				latest = sent
			}
		}
		if now.Sub(latest) < p.minimum {
			return false
		}
	}
	return true
}

func (p *HeartbeatPolicy) Record(ctx context.Context, store ports.StateStore, now time.Time) error {
	state, err := store.Load(ctx)
	if err != nil {
		return err
	}
	if !p.CanSend(state, now) {
		return errors.New("proactive heartbeat is throttled")
	}
	_, err = store.Update(ctx, state.Version, func(state *ports.State) error {
		cutoff := now.Add(-7 * 24 * time.Hour)
		kept := state.ProactiveMessages[:0]
		for _, sent := range state.ProactiveMessages {
			if sent.After(cutoff) {
				kept = append(kept, sent)
			}
		}
		state.ProactiveMessages = append(kept, now)
		return nil
	})
	return err
}

func HeartbeatActionAllowed(action string) bool {
	switch action {
	case "commit", "push", "create_pull_request", "calendar_create", "calendar_update", "calendar_delete":
		return false
	default:
		return true
	}
}
