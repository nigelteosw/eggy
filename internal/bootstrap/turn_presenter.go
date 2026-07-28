package bootstrap

import (
	"context"
	"strings"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
	"github.com/nigelteosw/eggy/plugins/channels/channelutil"
)

// turnPresenter is the surface-side rendering internal/kernel/turns asks for
// (turns.Presenter). It lives here rather than in the kernel for two reasons:
// the kernel may not import plugins/, and a typing hint and an edited-in-place
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
// that surface a live "Calling <tool>..." indicator, editing one message in
// place as more tools are called during the turn -- the same
// DeliverTrackable/EditText mechanism a coding run's progress uses, reused
// here so an ordinary tool call (e.g. current_time) is visible mid-turn too,
// not folded silently into the final reply. finish is always safe to call, a
// no-op if no tool was ever called.
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
		render("Calling " + strings.Join(calls, ", ") + "...")
	}
	finish = func() {
		if len(calls) == 0 {
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
