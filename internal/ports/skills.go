package ports

import (
	"context"
	"errors"
)

// ErrSkillNotFound is what a skills store returns for a name with no file, so
// the panel can answer 404 without matching message text.
var ErrSkillNotFound = errors.New("skill does not exist")

// SkillSummary is the compact, always-in-context view of one installed
// skill: enough for the agent to decide whether to load its full body with
// skill_read, without paying for that body on every turn.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Disabled is the owner's frontmatter choice; a disabled skill is kept
	// out of the model's index and refused by skill_read.
	Disabled bool `json:"disabled"`
	// Invalid says why a file in the skills directory cannot be used, so the
	// panel can show it instead of silently hiding it. Empty for usable skills.
	Invalid string `json:"invalid,omitempty"`
}

// SkillIndexBudget bounds the summaries the model sees each turn. It is
// applied to enabled skills only, after filtering, so disabled ones never
// crowd active ones out of the index.
const SkillIndexBudget = 16 << 10

// Skill is one installed skill's full content, returned only when fetched
// by name (skill_read, /skills show), never held resident across a whole
// turn's context the way SkillSummary is.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Disabled    bool   `json:"disabled"`
}

// SkillsStore persists procedural skills as one Markdown file per skill.
// Write is deliberately create-or-replace, whole-file: unlike ContextStore's
// section-addressed edits, a skill has no durable internal structure worth
// patching in place. Nothing in this port executes a skill; it only reads
// and writes its Markdown text.
type SkillsStore interface {
	List(context.Context) ([]SkillSummary, error)
	Read(context.Context, string) (Skill, error)
	// Write creates or replaces a skill and keeps the disabled state of the
	// file it replaces, so editing a skill never re-enables it.
	Write(context.Context, string, string, string) error
	SetDisabled(context.Context, string, bool) error
	Delete(context.Context, string) error
}
