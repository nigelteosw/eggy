package ports

// CapabilityReport is the deterministic answer to /capabilities: what this
// process actually wired at boot, narrowed by persisted runtime state. It
// carries no credential, environment content, or path -- only names and
// readiness flags.
type CapabilityReport struct {
	ActiveModel     string `json:"active_model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// Repositories are the configured repository names; SelfRepository is the
	// one holding Eggy's own source, or "" when none is marked.
	Repositories   []string `json:"repositories,omitempty"`
	SelfRepository string   `json:"self_repository,omitempty"`
	// Integrations are the optional adapters bootstrap actually built, by
	// name -- never their configuration.
	Integrations []string `json:"integrations,omitempty"`
	// Tools is the tool set an ordinary owner-prompted turn carries.
	Tools []string `json:"tools,omitempty"`
	// The readiness flags are the implementation loop's shipping chain.
	RepositoryCommitReady bool `json:"repository_commit_ready"`
	RepositoryPushReady   bool `json:"repository_push_ready"`
	PullRequestReady      bool `json:"pull_request_ready"`
	CalendarEnabled       bool `json:"calendar_enabled"`
}

// ContextSection is one contributor to what a turn puts in front of the
// model, measured in bytes of rendered content.
type ContextSection struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
	// MaxBytes is the write budget enforced on this section's source, or 0
	// when it has none. Only the two agent-curated documents are budgeted.
	MaxBytes int64 `json:"max_bytes,omitempty"`
}

// ContextReport is the deterministic accounting behind /context: what is
// resident in a turn's context right now, and the bounds that decide when it
// gets truncated or compacted. Every number is measured from persisted state
// or the same assembly the turn itself uses -- nothing here is estimated
// except EstimatedTokens, which is explicitly labelled as such.
type ContextReport struct {
	// Sections are the injected system instructions, the registered tool
	// schemas, and the live conversation window, in that order.
	Sections []ContextSection `json:"sections"`
	// RecentMessages is how many messages the conversation window holds now.
	RecentMessages int `json:"recent_messages"`
	// The remaining fields are the active agent.ContextPolicy: BudgetChars
	// bounds loop-generated messages before a turn compacts at a checkpoint,
	// RecentSteps bounds how many tool-calling steps stay live,
	// OutputExcerptChars bounds one message's contribution (the truncation
	// marker owners actually see), and MaxSteps is the runaway guard.
	BudgetChars        int `json:"budget_chars"`
	RecentSteps        int `json:"recent_steps"`
	OutputExcerptChars int `json:"output_excerpt_chars"`
	MaxSteps           int `json:"max_steps"`
}

// ResidentBytes totals every section.
func (r ContextReport) ResidentBytes() int {
	total := 0
	for _, section := range r.Sections {
		total += section.Bytes
	}
	return total
}
