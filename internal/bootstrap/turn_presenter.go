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
		text := progressLine(calls)
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
		// A progress line that can be removed is removed: the reply is the answer,
		// and the steps are not worth keeping in the chat. A channel that cannot
		// delete it keeps the one-line summary.
		if err := channelutil.DeleteText(ctx, p.channel, messageID); err == nil {
			return
		}
		render(progressDone(calls))
	}
	return onToolCall, finish
}

// DeliverOutcome reports an approve/reject outcome, editing the original
// message when the surface supports it.
func (p turnPresenter) DeliverOutcome(ctx context.Context, messageID, text string) error {
	return channelutil.DeliverOutcome(ctx, p.channel, messageID, text)
}

// toolProgressLabels says, in the owner's words, what each tool is doing. A tool
// with no entry reads as "Using" plus its name spaced out, never as a bare
// identifier.
var toolProgressLabels = map[string]string{
	"web_search":          "Searching the web",
	"web_extract":         "Reading a web page",
	"current_time":        "Checking the time",
	"memory":              "Updating what I remember",
	"recall_conversation": "Looking back at earlier conversations",
	"read_file":           "Reading a file",
	"schedule":            "Looking at your schedules",
	"skill_read":          "Reading a skill",
	"finance":             "Checking your finances",
	"agent_runner":        "Checking on a coding run",
	"google_gmail":        "Checking email",
	"google_calendar":     "Checking your calendar",
	"google_drive":        "Looking in Drive",
	"google_docs":         "Reading a document",
	"google_sheets":       "Reading a spreadsheet",
	"google_contacts":     "Looking up a contact",
	"repository_github":   "Checking GitHub",
	"repository_list":     "Looking at repositories",
	"workspace_open":      "Opening a workspace",
	"workspace_close":     "Closing a workspace",
	"status":              "Checking Eggy's status",
}

func toolProgressLabel(name string) string {
	if label, ok := toolProgressLabels[name]; ok {
		return label
	}
	return "Using " + strings.ReplaceAll(name, "_", " ")
}

// progressLine is the status while a turn works: each distinct action once, in
// the order it started.
func progressLine(calls []string) string {
	return progressLabels(calls) + "…"
}

// progressDone is what remains once the turn has finished with its tools.
func progressDone(calls []string) string {
	return progressLabels(calls) + "."
}

func progressLabels(calls []string) string {
	seen := map[string]bool{}
	var labels []string
	for _, name := range calls {
		label := toolProgressLabel(name)
		if seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	return strings.Join(labels, ", ")
}
