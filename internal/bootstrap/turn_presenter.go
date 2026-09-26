package bootstrap

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/channel/channelutil"
	"github.com/nigelteosw/eggy/internal/ports"
)

// turnPresenter is the surface-side rendering internal/core/turns asks for
// (turns.Presenter). It lives here rather than in the core for two reasons:
// the core may not import a provider package, and a typing hint and an edited-in-place
// "Calling X..." message are affordances a channel either has or doesn't --
// channelutil is exactly the code that degrades gracefully when it doesn't.
type turnPresenter struct {
	channel ports.Channel
}

// StartTyping shows work-in-progress on whichever channel ctx resolves to,
// and returns the function that stops it. A no-op on a channel with no
// typing indicator.
func (p turnPresenter) StartTyping(ctx context.Context) func() {
	return channelutil.StartTyping(ctx, p.channel, 4*time.Second)
}

// ShowToolCalls returns a tool-call callback and a matching finish function
// that surface a live "Calling <tool>..." indicator. On a channel with a
// status line (ports.ProgressChannel -- the browser, which draws it beside
// its typing dots) the status stays out of the conversation and the final
// reply simply supersedes it. Elsewhere it is one message edited in place as
// more tools are called -- the same DeliverTrackable/EditText mechanism a
// coding run's progress uses, reused here so an ordinary tool call (e.g.
// current_time) is visible mid-turn too, not folded silently into the final
// reply. finish is always safe to call, a no-op if no tool was ever called.
func (p turnPresenter) ShowToolCalls(ctx context.Context) (onToolCall func(string), finish func()) {
	var messageID string
	var calls []string
	render := func(text string) {
		if messageID != "" && channelutil.EditText(ctx, p.channel, messageID, text) == nil {
			return
		}
		if id, err := channelutil.DeliverTrackable(ctx, p.channel, text); err == nil {
			messageID = id
		}
	}
	onToolCall = func(name string) {
		calls = append(calls, name)
		text := "Calling " + strings.Join(calls, ", ") + "..."
		if err := channelutil.ShowProgress(ctx, p.channel, text); !errors.Is(err, channelutil.ErrProgressUnsupported) {
			return
		}
		render(text)
	}
	finish = func() {
		// Only the message form needs settling: a status line has nothing
		// left to say once the reply that replaces it is on its way.
		if messageID == "" {
			return
		}
		render("Called " + strings.Join(calls, ", ") + ".")
	}
	return onToolCall, finish
}

// DeliverOutcome reports an approve/reject outcome, editing the original
// message when the surface supports it.
func (p turnPresenter) DeliverOutcome(ctx context.Context, messageID, text string) error {
	return channelutil.DeliverOutcome(ctx, p.channel, messageID, text)
}
