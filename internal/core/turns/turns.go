// Package turns owns what happens during one turn: its tools, context,
// persistence, and surface delivery.
//
// It exists because that is core agentic behavior rather than wiring. It used
// to live in internal/bootstrap, the composition root, where no kernel test
// could guard it -- and where the safety-relevant part (which turns are
// unprompted, and what those turns may reach) sat next to HTTP clients and
// adapter selection.
//
// Every collaborator is a narrow interface declared here rather than a whole
// service, for the same reason internal/commands and internal/panel declare
// theirs: a turn should not be able to reach past what it needs. Presentation
// stays outside -- the core may not import a provider package, and the typing hint and
// live "Calling X..." indicator are surface affordances anyway. They arrive as
// the Presenter interface.
//
// Files: turns.go holds the three entry points (owner, scheduled, heartbeat);
// policy.go what differs between them and which tools an unprompted turn may
// reach; run.go the shared turn body; approval.go resuming a turn after an
// approval decision; collaborators.go the interfaces a Service is built from.
package turns

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/core/agent"
	"github.com/nigelteosw/eggy/internal/core/services"
	"github.com/nigelteosw/eggy/internal/ports"
)

// Service runs turns. One instance serves every surface: Telegram and web are
// peers that each only decide which entry point to call.
//
// It embeds Options rather than restating all nineteen collaborators as
// lowercase fields and copying them across one at a time. The two lists were
// identical apart from case, so the only thing the copy bought was a third
// place to forget a field when adding one. A Service is built once by New and
// never written again.
type Service struct {
	Options
}

func New(options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Location == nil {
		options.Location = time.UTC
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Service{Options: options}
}

// OwnerMessage runs a direct owner turn: the complete tool set, ambient
// recent-conversation history, and a recorded exchange. It is the only kind
// of turn a later owner message can steer.
func (s *Service) OwnerMessage(ctx context.Context, message ports.Message, source string) error {
	if strings.TrimSpace(source) == "" {
		source = "telegram"
	}
	return s.run(ctx, message, agent.RunOptions{}, Policy{
		IncludeRecentHistory: true,
		RecordConversation:   true,
		Source:               source,
		Kind:                 ports.TraceKindOwner,
	})
}

// ScheduledTurn runs a turn the owner scheduled but is not present for. It is
// self-contained: no ambient recent-conversation history, so an owner's
// earlier chat cannot silently steer instructions they never reviewed at the
// time this schedule fires. It is marked unprompted, which is what confines
// it to proposing.
func (s *Service) ScheduledTurn(ctx context.Context, text string) error {
	return s.run(ctx, ports.Message{Role: ports.RoleUser, Content: text}, ReadOnlyTools(), Policy{
		Extra: []ports.Message{agent.ScheduledTurnMessage()},
		Kind:  ports.TraceKindScheduled,
	})
}

// HeartbeatTurn is a periodic check-in the owner is not present for. Its
// isolation is ScheduledTurn's, unchanged -- the same read-only allowlist and
// no ambient conversation history -- and the only difference is that it is
// allowed to conclude there is nothing worth saying.
// It carries the watch list and heartbeat_respond, which together are what
// let it conclude that it has already said this.
//
// The response is attached to the context and deliberately not stored on the
// Service: one Service instance serves every surface, so a field would be
// shared mutable state across turns. run reads it back off the context.
// includeRecentHistory relaxes the no-ambient-history rule, and is the
// owner's explicit choice rather than a default: see
// config.HeartbeatConfig.IncludeRecentHistory. The allowlist is unchanged
// either way, so this changes what a beat knows and never what it can do.
// The response is returned so the caller can act on what the beat decided --
// today, when it asked to be woken next. A beat that failed or never called
// the tool returns a zero response, which the caller reads as "no decision".
func (s *Service) HeartbeatTurn(ctx context.Context, text string, includeRecentHistory bool) (services.HeartbeatResponse, error) {
	ctx, response := services.WithHeartbeatResponse(ctx)
	err := s.run(ctx, ports.Message{Role: ports.RoleUser, Content: text}, heartbeatTools(), Policy{
		Extra:                []ports.Message{agent.HeartbeatTurnMessage()},
		SuppressSilentReply:  true,
		IncludeWatchDocument: true,
		IncludeRecentHistory: includeRecentHistory,
		Kind:                 ports.TraceKindHeartbeat,
	})
	return *response, err
}

// Active reports whether a turn is currently executing.
func (s *Service) Active() bool { return s.Registry.Active() }
