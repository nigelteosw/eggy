package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

func handleCalendarAuth(ctx context.Context, s *CommandService, req CommandRequest) (CommandResult, error) {
	if !s.config.Calendar.Enabled {
		return CommandResult{State: ResultInfo, Title: "Calendar is not configured."}, nil
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return CommandResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	digest := sha256.Sum256([]byte(token))
	state, err := s.store.Load(ctx)
	if err != nil {
		return CommandResult{}, err
	}
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	_, err = s.store.Update(ctx, state.Version, func(state *ports.State) error {
		state.Calendar.EnrollmentDigest = hex.EncodeToString(digest[:])
		state.Calendar.EnrollmentExpires = now().Add(10 * time.Minute)
		return nil
	})
	if err != nil {
		return CommandResult{}, err
	}
	link := s.config.Server.PublicBaseURL + "/auth/google?enrollment=" + url.QueryEscape(token)
	return CommandResult{
		Title:  "Google Calendar enrollment started.",
		Detail: "This link authorizes Eggy to read and write your Google Calendar. It is single-use and expires in 10 minutes.",
		Fields: []ResultField{{Label: "Enrollment link", Value: link}},
	}, nil
}
