package services

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/destination"
	"github.com/nigelteosw/eggy/internal/ports"
)

// ChecksCompletion is one pull request whose checks have finished badly,
// paired with the thread whose workspace still holds the branch they ran
// against. It is what closes the self-improvement loop: without it a
// proposed change is one-shot, and the failure only ever reaches the owner's
// GitHub notifications.
type ChecksCompletion struct {
	Session           string
	Repository        string
	Branch            string
	Ref               string
	PullRequestURL    string
	PullRequestNumber int
	Conclusion        string
	// Destination is the thread that proposed the change, so the resumed
	// turn lands in the conversation that owns the still-open workspace
	// rather than on a default surface.
	Destination destination.Destination
	// Evidence is the failing checks, rendered for the turn's instruction.
	Evidence []ports.CheckRun
}

// ChecksWatcher polls the pull requests Eggy has open for check results and
// reports the ones that failed while their thread's workspace is still
// attached. It reads through ports.RepositoryReader.Checks -- the same read
// path repository_github's "checks" kind uses -- rather than adding a second
// GitHub surface, and it never mutates anything on GitHub.
type ChecksWatcher struct {
	store    ports.StateStore
	sessions *ImplementationSessions
	threads  ports.ThreadStore
	reader   ports.RepositoryReader
}

func NewChecksWatcher(store ports.StateStore, sessions *ImplementationSessions, threads ports.ThreadStore, reader ports.RepositoryReader) *ChecksWatcher {
	return &ChecksWatcher{store: store, sessions: sessions, threads: threads, reader: reader}
}

// Poll returns every newly failed check result worth resuming a thread for,
// and records each one against its session so the same failure is reported
// exactly once. A successful or still-running suite produces nothing: there
// is nothing for the agent to do about a green pull request, and saying so
// unprompted would just be noise.
func (w *ChecksWatcher) Poll(ctx context.Context) ([]ChecksCompletion, error) {
	if w == nil || w.sessions == nil || w.threads == nil || w.reader == nil {
		return nil, nil
	}
	sessions, err := w.sessions.List(ctx)
	if err != nil {
		return nil, err
	}
	// Only a session whose thread still has the workspace attached can be
	// resumed in place; once the checkout is reaped there is no branch to
	// fix the failure in, and a new turn would be a fresh clone the owner
	// never asked for.
	attached, err := w.threads.ThreadsWithWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	bySession := make(map[string]ports.Thread, len(attached))
	for _, thread := range attached {
		if thread.WorkspaceSession != "" {
			bySession[thread.WorkspaceSession] = thread
		}
	}
	completions := make([]ChecksCompletion, 0)
	for _, session := range sessions {
		if session.PullRequestNumber <= 0 || session.Commit == "" || session.Repository == "" {
			continue
		}
		thread, ok := bySession[session.ID]
		if !ok {
			continue
		}
		if session.ChecksRef == session.Commit && session.ChecksConclusion != "" {
			continue
		}
		repository, err := lookupRepository(ctx, w.store, session.Repository)
		if err != nil {
			// A repository that has since been deregistered is not a reason
			// to abandon the other sessions' checks.
			continue
		}
		checks, err := w.reader.Checks(ctx, repository, session.Commit)
		if err != nil {
			return completions, err
		}
		conclusion, failed, settled := summarizeChecks(checks)
		if !settled {
			continue
		}
		if err := w.sessions.RecordChecks(ctx, session.ID, session.Commit, conclusion); err != nil {
			return completions, err
		}
		if conclusion == "success" {
			continue
		}
		completions = append(completions, ChecksCompletion{
			Session: session.ID, Repository: session.Repository, Branch: session.Branch,
			Ref: session.Commit, PullRequestURL: session.PullRequestURL, PullRequestNumber: session.PullRequestNumber,
			Conclusion: conclusion, Destination: destinationOfThread(thread), Evidence: failed,
		})
	}
	sort.Slice(completions, func(i, j int) bool { return completions[i].Session < completions[j].Session })
	return completions, nil
}

// destinationOfThread reconstructs the surface a thread belongs to. A thread
// records the channel it was opened from when its workspace was attached, so
// a resumed turn replies where the change was proposed.
func destinationOfThread(thread ports.Thread) destination.Destination {
	if thread.Channel == destination.Web {
		return destination.Destination{Kind: destination.Web, ThreadID: thread.ID}
	}
	return destination.Destination{Kind: destination.Telegram}
}

// summarizeChecks reduces a commit's check runs to one conclusion. settled
// is false while any run is still queued or in progress: a partial suite is
// not a result, and reacting to one would send the agent chasing a failure
// the next run may already be fixing.
func summarizeChecks(checks []ports.CheckRun) (conclusion string, failed []ports.CheckRun, settled bool) {
	if len(checks) == 0 {
		return "", nil, false
	}
	for _, check := range checks {
		if !strings.EqualFold(check.Status, "completed") {
			return "", nil, false
		}
		switch strings.ToLower(check.Conclusion) {
		case "success", "neutral", "skipped":
		default:
			failed = append(failed, check)
		}
	}
	if len(failed) == 0 {
		return "success", nil, true
	}
	return "failure", failed, true
}

// ChecksInstruction renders the turn instruction a failed suite produces. It
// is an ordinary owner-shaped request against the thread that already has
// the branch open, not a special mode: the agent reads the failing checks,
// fixes them with the same primitives, and proposes again.
func (c ChecksCompletion) ChecksInstruction() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pull request #%d on %s (branch %s) has failing checks", c.PullRequestNumber, c.Repository, c.Branch)
	if c.PullRequestURL != "" {
		fmt.Fprintf(&b, " — %s", c.PullRequestURL)
	}
	b.WriteString(".\n\nFailing checks:\n")
	for _, check := range c.Evidence {
		fmt.Fprintf(&b, "- %s: %s", check.Name, strings.ToLower(check.Conclusion))
		if check.URL != "" {
			fmt.Fprintf(&b, " (%s)", check.URL)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nThis conversation's workspace is still on that branch. Investigate the failure (repository_github with kind \"checks\" and ref " + c.Ref + " re-reads them), fix it with the usual tools, re-run this repository's own build/test commands, and propose the change again so the same pull request is updated. If the failure is not something you should fix unattended, say so and stop.")
	return b.String()
}

// ChecksEventID is the stable, idempotent identifier for one resumption: the
// session and the exact commit its checks ran against.
func (c ChecksCompletion) ChecksEventID() string {
	return "checks:" + c.Session + ":" + c.Ref
}
