package services

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

// Diagnostics measures what a turn actually puts in front of the model. It
// answers /context, and it exists as a kernel service rather than as command
// code because the only honest way to report a turn's context is to assemble
// it exactly the way the turn does -- through agent.Instructions and the
// loop's own filtered tool set -- instead of re-deriving it and drifting.
//
// It is read-only by construction: every dependency below is a reader.
type Diagnostics struct {
	context      ports.ContextStore
	store        ports.StateStore
	runtime      modelSelector
	skills       skillIndex
	conversation messageWindow
	loop         toolLister
	manifest     agent.CapabilityManifest
	policy       agent.ContextPolicy
	integrations []string
}

type modelSelector interface {
	SelectedModel(ctx context.Context) (string, error)
	ReasoningEffort(ctx context.Context) (string, error)
}

type skillIndex interface {
	Enabled(ctx context.Context) ([]ports.SkillSummary, error)
}

type messageWindow interface {
	RecentMessages(ctx context.Context, conversationID string) ([]ports.Message, error)
}

type toolLister interface {
	ToolDefinitions(options agent.RunOptions) []ports.ToolDefinition
}

// DiagnosticsOptions carries Diagnostics' readers. A nil reader is tolerated:
// the section it would have measured is reported as zero rather than failing
// the whole report.
type DiagnosticsOptions struct {
	Context      ports.ContextStore
	Store        ports.StateStore
	Runtime      modelSelector
	Skills       skillIndex
	Conversation messageWindow
	Loop         toolLister
	Manifest     agent.CapabilityManifest
	Policy       agent.ContextPolicy
	// Integrations are the optional adapters bootstrap actually built, by
	// name. Bootstrap passes what it wired rather than what config asked for,
	// so a misconfigured integration cannot report itself as enabled.
	Integrations []string
}

func NewDiagnostics(options DiagnosticsOptions) *Diagnostics {
	return &Diagnostics{
		context: options.Context, store: options.Store, runtime: options.Runtime,
		skills: options.Skills, conversation: options.Conversation, loop: options.Loop,
		manifest: options.Manifest, policy: options.Policy, integrations: options.Integrations,
	}
}

// CapabilityReport is the boot-time capability set narrowed by persisted
// state: the same manifest a turn starting now would be given.
func (d *Diagnostics) CapabilityReport(ctx context.Context) (ports.CapabilityReport, error) {
	manifest, err := d.currentManifest(ctx)
	if err != nil {
		return ports.CapabilityReport{}, err
	}
	report := ports.CapabilityReport{
		ActiveModel:           manifest.ActiveModel,
		Repositories:          manifest.Repositories,
		SelfRepository:        manifest.SelfRepository,
		Integrations:          d.integrations,
		Tools:                 manifest.Tools,
		RepositoryCommitReady: manifest.RepositoryCommitReady,
		RepositoryPushReady:   manifest.RepositoryPushReady,
		PullRequestReady:      manifest.PullRequestReady,
		CalendarEnabled:       manifest.CalendarEnabled,
	}
	sort.Strings(report.Repositories)
	sort.Strings(report.Tools)
	if d.runtime != nil {
		effort, err := d.runtime.ReasoningEffort(ctx)
		if err != nil {
			return ports.CapabilityReport{}, err
		}
		report.ReasoningEffort = effort
	}
	return report, nil
}

// ContextReport measures one ordinary owner-prompted turn in conversationID:
// the system instructions section by section, the tool schemas that turn
// would send, and the live conversation window.
func (d *Diagnostics) ContextReport(ctx context.Context, conversationID string) (ports.ContextReport, error) {
	report := ports.ContextReport{
		BudgetChars:        d.policy.BudgetChars,
		RecentSteps:        d.policy.RecentSteps,
		OutputExcerptChars: d.policy.OutputExcerptChars,
		MaxSteps:           d.policy.MaxSteps,
	}
	agentContext := ports.AgentContext{}
	if d.context != nil {
		loaded, err := d.context.Load(ctx)
		if err != nil {
			return ports.ContextReport{}, err
		}
		agentContext = loaded
	}
	manifest, err := d.currentManifest(ctx)
	if err != nil {
		return ports.ContextReport{}, err
	}
	// TemporalContext is left at its zero time: it renders a fixed-width
	// timestamp, so the section's size does not depend on when /context runs.
	for _, section := range agent.Instructions(agentContext, manifest, agent.TemporalContext{}) {
		measured := ports.ContextSection{Name: section.Name, Bytes: len(section.Message.Content)}
		switch section.Name {
		case "USER.md":
			measured.MaxBytes = agentContext.UserMaxBytes
		case "MEMORY.md":
			measured.MaxBytes = agentContext.MemoryMaxBytes
		}
		report.Sections = append(report.Sections, measured)
	}
	report.Sections = append(report.Sections, ports.ContextSection{Name: "tool schemas", Bytes: d.toolSchemaBytes()})
	if d.conversation != nil {
		recent, err := d.conversation.RecentMessages(ctx, conversationID)
		if err != nil {
			return ports.ContextReport{}, err
		}
		bytes := 0
		for _, message := range recent {
			bytes += len(message.Content)
		}
		report.RecentMessages = len(recent)
		report.Sections = append(report.Sections, ports.ContextSection{Name: "recent history", Bytes: bytes})
	}
	return report, nil
}

// ToolNames is the tool set an ordinary owner-prompted turn carries, which is
// what /capabilities reports.
func (d *Diagnostics) ToolNames() []string {
	definitions := d.toolDefinitions()
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func (d *Diagnostics) toolDefinitions() []ports.ToolDefinition {
	if d.loop == nil {
		return nil
	}
	return d.loop.ToolDefinitions(agent.RunOptions{})
}

func (d *Diagnostics) toolSchemaBytes() int {
	total := 0
	for _, definition := range d.toolDefinitions() {
		encoded, err := json.Marshal(definition)
		if err != nil {
			continue
		}
		total += len(encoded)
	}
	return total
}

// currentManifest mirrors turns.Service.capabilityManifest: the boot-time
// manifest narrowed by persisted state, so the measured section is the one a
// turn starting now would inject.
func (d *Diagnostics) currentManifest(ctx context.Context) (agent.CapabilityManifest, error) {
	manifest := d.manifest
	if d.runtime != nil {
		alias, err := d.runtime.SelectedModel(ctx)
		if err != nil {
			return agent.CapabilityManifest{}, err
		}
		manifest.ActiveModel = alias
	}
	if d.store != nil {
		state, err := d.store.Load(ctx)
		if err != nil {
			return agent.CapabilityManifest{}, err
		}
		manifest.Repositories = make([]string, 0, len(state.Repositories))
		for name := range state.Repositories {
			manifest.Repositories = append(manifest.Repositories, name)
		}
		configured := len(manifest.Repositories) > 0
		manifest.RepositoryCommitReady = configured && manifest.RepositoryCommitReady
		manifest.RepositoryPushReady = configured && manifest.RepositoryPushReady
		manifest.PullRequestReady = configured && manifest.PullRequestReady
	}
	if d.skills != nil {
		enabled, err := d.skills.Enabled(ctx)
		if err != nil {
			return agent.CapabilityManifest{}, err
		}
		manifest.Skills = make([]agent.SkillDescriptor, 0, len(enabled))
		for _, skill := range enabled {
			manifest.Skills = append(manifest.Skills, agent.SkillDescriptor{Name: skill.Name, Description: skill.Description})
		}
	}
	manifest.Tools = d.ToolNames()
	return manifest, nil
}
