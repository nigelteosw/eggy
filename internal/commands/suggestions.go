package commands

import (
	"context"
	"regexp"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

// SkillLister is the part of the skills service the slash list reads.
type SkillLister interface {
	List(context.Context) ([]ports.SkillSummary, error)
}

// Suggestion is one entry in the slash list a composer offers. Kind is
// "command" or "skill".
type Suggestion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

// Suggestions is the one slash list: every command this deployment answers,
// then every enabled skill. Telegram's / menu and the web composer both draw
// it, so the two surfaces cannot offer different things.
func (s *CommandService) Suggestions(ctx context.Context) []Suggestion {
	out := make([]Suggestion, 0, len(telegramCommands)+2)
	for _, command := range telegramCommands {
		if command.Name == "plugin" && s.Marketplace == nil {
			continue
		}
		out = append(out, Suggestion{Name: command.Name, Description: command.Description, Kind: "command"})
	}
	if s.Code != nil {
		out = append(out, Suggestion{Name: "code", Description: codeDescription, Kind: "command"})
	}
	for _, skill := range s.enabledSkills(ctx) {
		description := skill.Description
		if description == "" {
			description = "Use the " + skill.Name + " skill"
		}
		out = append(out, Suggestion{Name: skill.Name, Description: description, Kind: "skill"})
	}
	return out
}

const codeDescription = "Run a coding task on an allowed repository"

// enabledSkills lists the skills a turn may load. A list failure costs only the
// suggestions, so it is an empty list rather than an error.
func (s *CommandService) enabledSkills(ctx context.Context) []ports.SkillSummary {
	if s.Skills == nil {
		return nil
	}
	all, err := s.Skills.List(ctx)
	if err != nil {
		return nil
	}
	out := all[:0:0]
	for _, skill := range all {
		if !skill.Disabled && skill.Invalid == "" {
			out = append(out, skill)
		}
	}
	return out
}

// skillNamed reports whether name, as typed, is an enabled skill. Telegram
// command names cannot hold a hyphen, so its menu offers pdf_tools for
// pdf-tools and either spelling is accepted.
func (s *CommandService) skillNamed(ctx context.Context, name string) bool {
	for _, skill := range s.enabledSkills(ctx) {
		if skill.Name == name || telegramCommandName(skill.Name) == name {
			return true
		}
	}
	return false
}

var telegramNameValid = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// telegramCommandName is a name as Telegram's menu accepts it, or empty when
// it cannot be spelled there.
func telegramCommandName(name string) string {
	name = strings.ReplaceAll(strings.ToLower(name), "-", "_")
	if !telegramNameValid.MatchString(name) {
		return ""
	}
	return name
}

// TelegramMenu is Suggestions spelled for Telegram's / menu: hyphens become
// underscores, a name Telegram cannot hold is left out, and descriptions are
// clipped to its limit.
func TelegramMenu(suggestions []Suggestion) []Suggestion {
	out := make([]Suggestion, 0, len(suggestions))
	seen := map[string]bool{}
	for _, suggestion := range suggestions {
		name := telegramCommandName(suggestion.Name)
		if name == "" || seen[name] || len(out) == 100 {
			continue
		}
		seen[name] = true
		suggestion.Name = name
		suggestion.Description = clip(suggestion.Description, 256)
		out = append(out, suggestion)
	}
	return out
}
