package ports

import (
	"context"
	"errors"
)

// ErrSkillNotFound is what a skills store returns for a name with no file, so
// the panel can answer 404 without matching message text.
var ErrSkillNotFound = errors.New("skill does not exist")

// ErrSkillExists is what a skills store returns when a bundle would create a
// skill whose name is already taken and the caller did not ask to replace it.
var ErrSkillExists = errors.New("skill already exists")

// SkillSummary is the compact, always-in-context view of one installed
// skill: enough for the agent to decide whether to load its full body with
// skill_read, without paying for that body on every turn.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Disabled is the owner's frontmatter choice; a disabled skill is kept
	// out of the model's index and refused by skill_read.
	Disabled bool `json:"disabled"`
	// Source names where a marketplace-installed skill came from. Empty means
	// hand-placed by the owner.
	Source string `json:"source,omitempty"`
	// Invalid says why a file in the skills directory cannot be used, so the
	// panel can show it instead of silently hiding it. Empty for usable skills.
	Invalid string `json:"invalid,omitempty"`
}

// SkillIndexBudget bounds the summaries the model sees each turn. It is
// applied to enabled skills only, after filtering, so disabled ones never
// crowd active ones out of the index. Every name is kept before any
// description is added; a description that does not fit is omitted.
const SkillIndexBudget = 16 << 10

// SkillNameOnlyHint stands in for a description the index could not fit. The
// model still sees the name and knows it can load the body with skill_read.
const SkillNameOnlyHint = "(description omitted; call skill_read)"

// Skill is one installed skill's full content, returned only when fetched
// by name (skill_read, /skills show), never held resident across a whole
// turn's context the way SkillSummary is.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Disabled    bool   `json:"disabled"`
	// Source is empty for hand-placed skills.
	Source string `json:"source,omitempty"`
	// Files lists the sorted relative paths of a directory skill's reference
	// files. Nil for a flat skill. Reference files are text the agent may read
	// through ReadFile; Eggy never executes them.
	Files []string `json:"files,omitempty"`
}

// SkillBundle is a whole skill to write at once: the SKILL.md fields plus any
// reference files, which make it a directory skill. Replace allows overwriting
// an existing skill of either layout, keeping its disabled state.
type SkillBundle struct {
	Name        string
	Description string
	Body        string
	Source      string
	Extra       map[string]any
	Files       map[string]string
	Replace     bool
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
	// ReadFile returns one reference file of a directory skill. The path is
	// relative to the skill directory and must be an allowlisted text file.
	ReadFile(ctx context.Context, name, file string) (string, error)
	// WriteBundle writes a whole skill, creating it or, with Replace set,
	// replacing it as one step.
	WriteBundle(context.Context, SkillBundle) error
}
