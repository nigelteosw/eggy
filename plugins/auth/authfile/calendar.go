package authfile

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

const (
	calendarSection = "calendar"
	calendarKey     = "google"
)

// CalendarStore adapts auth.json to ports.CalendarAuthStore. The refresh
// token inside the record is already sealed by google.TokenCipher, so this
// layer only moves an opaque record in and out of the shared document.
type CalendarStore struct{ file *Store }

// Calendar returns the Google Calendar credential view of this auth file.
func (s *Store) Calendar() *CalendarStore { return &CalendarStore{file: s} }

func (c *CalendarStore) Load(ctx context.Context) (ports.CalendarAuth, error) {
	if err := ctx.Err(); err != nil {
		return ports.CalendarAuth{}, err
	}
	body, err := c.file.Read(calendarSection, calendarKey)
	if errors.Is(err, ErrNotFound) {
		return ports.CalendarAuth{}, nil
	}
	if err != nil {
		return ports.CalendarAuth{}, err
	}
	var auth ports.CalendarAuth
	if err := json.Unmarshal(body, &auth); err != nil {
		return ports.CalendarAuth{}, errors.New("invalid Google Calendar auth record")
	}
	return auth, nil
}

func (c *CalendarStore) Update(ctx context.Context, mutate func(*ports.CalendarAuth) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.file.Update(calendarSection, calendarKey, func(body json.RawMessage) (json.RawMessage, error) {
		var auth ports.CalendarAuth
		if body != nil {
			if err := json.Unmarshal(body, &auth); err != nil {
				return nil, errors.New("invalid Google Calendar auth record")
			}
		}
		if err := mutate(&auth); err != nil {
			return nil, err
		}
		return json.Marshal(auth)
	})
}
