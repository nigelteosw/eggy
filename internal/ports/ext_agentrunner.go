package ports

import (
	"context"
	"errors"
	"time"
)

// ErrAgentRunNotFound is returned for a run id that does not exist for the
// acting account. Another account's run is indistinguishable from a missing
// one on purpose, so a caller cannot probe which ids exist.
var ErrAgentRunNotFound = errors.New("agent run not found")

// ErrAgentRunActive reports a delete of a run that is still queued, running,
// waiting, or publishing. Only a finished run can be deleted.
var ErrAgentRunActive = errors.New("agent run is still in progress")

// Finished reports whether a run has stopped moving for good. Only a finished
// run may be deleted.
func (s AgentRunState) Finished() bool {
	switch s {
	case AgentRunCompleted, AgentRunFailed, AgentRunCancelled, AgentRunInterrupted:
		return true
	}
	return false
}

// ErrAgentRunInvalid wraps every refusal of a run request's contents or of the
// run's state: an unknown or disabled repository, a bad task, a missing token.
// The panel shows the message after the prefix; anything else is a server fault.
var ErrAgentRunInvalid = errors.New("invalid agent run request")

// ErrAgentRunQueued reports a second run started while the account already has
// one queued. Only one may wait for a worker at a time.
var ErrAgentRunQueued = errors.New("you already have a queued run; wait for it to start or cancel it")

// ErrAgentRunConflict reports an update written against a stale revision: the
// run changed since the caller read it, so the caller must re-read and decide
// again rather than overwrite.
var ErrAgentRunConflict = errors.New("agent run changed concurrently")

// AgentRunState is where a coding run is in its life. Transitions are owned
// by the agent runner service, which writes them conditionally.
type AgentRunState string

const (
	AgentRunQueued         AgentRunState = "queued"
	AgentRunRunning        AgentRunState = "running"
	AgentRunWaitingForUser AgentRunState = "waiting_for_user"
	AgentRunPublishing     AgentRunState = "publishing"
	AgentRunCompleted      AgentRunState = "completed"
	AgentRunFailed         AgentRunState = "failed"
	AgentRunCancelled      AgentRunState = "cancelled"
	AgentRunInterrupted    AgentRunState = "interrupted"
)

// PublishStage records how far publication got. Each stage is persisted before
// the external effect it names, so recovery never repeats a confirmed effect.
type PublishStage string

const (
	PublishNone         PublishStage = ""
	PublishCommitted    PublishStage = "committed"
	PublishPushIntended PublishStage = "push_intended"
	PublishPushed       PublishStage = "pushed"
	PublishPRIntended   PublishStage = "pr_intended"
	PublishPROpened     PublishStage = "pr_opened"
	PublishDelivered    PublishStage = "delivered"
)

// AgentRun is one coding run. Every run belongs to the account on the context
// it was created under; the owner is never a field, because nothing downstream
// may name which account a record is for. Output is already redacted before it
// is stored.
type AgentRun struct {
	ID string
	// AccountID is the owner stored on the run's row. The store sets it on
	// read; no caller can choose it on create, which takes the account from
	// the context. The worker runs as this account, the only way it can act on
	// a run without a request.
	AccountID      string
	Repository     string
	Brief          string
	BriefDigest    string // sha256 hex of Brief
	BaseBranch     string
	BaseSHA        string
	Branch         string // eggy/<ID>
	HeadSHA        string
	State          AgentRunState
	Revision       int64
	Stage          PublishStage
	PATGeneration  int64
	MandateExpires time.Time
	Question       string
	Answer         string
	UsedTokens     int64
	UsedActive     time.Duration
	Output         string
	PRURL          string
	// PRTitle, PRBody and Draft are fixed at commit time, so a crash after the
	// commit can still open the same pull request.
	PRTitle           string
	PRBody            string
	Draft             bool
	Failure           string
	OriginChannel     string
	OriginDestination string
	Delivered         bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// AgentRunStore is the agent runner's durable state. Account-facing methods
// take the account from the context and fail closed without one. The two
// worker methods take no account, because the single worker serves every
// account and is never driven by a request.
type AgentRunStore interface {
	CreateAgentRun(ctx context.Context, run AgentRun) error
	GetAgentRun(ctx context.Context, id string) (AgentRun, error)
	ListAgentRuns(ctx context.Context, limit int) ([]AgentRun, error)
	// UpdateAgentRun writes run only if the stored Revision equals run.Revision,
	// then returns it with the Revision incremented.
	UpdateAgentRun(ctx context.Context, run AgentRun) (AgentRun, error)
	SetRepositoryEnabled(ctx context.Context, repository string, enabled bool) error
	EnabledRepositories(ctx context.Context) ([]string, error)
	// NextQueuedAgentRun returns the oldest queued run across all accounts.
	NextQueuedAgentRun(ctx context.Context) (AgentRun, bool, error)
	// AgentRunsInStates returns runs in any of the given states, across accounts.
	AgentRunsInStates(ctx context.Context, states ...AgentRunState) ([]AgentRun, error)
	// DeleteAgentRun removes a finished run and its steps the acting account
	// owns. A run still in flight is refused with ErrAgentRunActive.
	DeleteAgentRun(ctx context.Context, id string) error
	// AppendAgentRunEvent records one step of a run the acting account owns,
	// numbered after the run's last event. A run that is not theirs is refused.
	AppendAgentRunEvent(ctx context.Context, runID string, event AgentRunEvent) error
	// ListAgentRunEvents returns a run's steps in the order they happened.
	ListAgentRunEvents(ctx context.Context, runID string) ([]AgentRunEvent, error)
}

// AgentRunEvent is one step in a run's record: a state or stage change, a
// tool call the coding model made, or the outcome of a check. Text is already
// redacted and bounded by the time it reaches the store.
type AgentRunEvent struct {
	Seq  int       `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}
