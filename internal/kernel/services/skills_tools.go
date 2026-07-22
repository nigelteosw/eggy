package services

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

// NewSkillTools returns the agent-callable half of the skills subsystem.
// skill_read is a plain read tool. skill_disable/skill_enable are also
// directly callable — unlike skill creation/editing/deletion, toggling never
// touches a skill's content, so it needs no approval gate (see
// SkillsService.Disable/Enable).
func NewSkillTools(skills *SkillsService) []ports.Tool {
	return []ports.Tool{
		skillReadTool{skills: skills},
		skillToggleTool{skills: skills, name: "skill_disable", enable: false,
			description: "Disable an installed skill so it stops appearing in the Available skills list; the file is untouched and it can be re-enabled anytime"},
		skillToggleTool{skills: skills, name: "skill_enable", enable: true,
			description: "Re-enable a previously disabled skill so it appears in the Available skills list again"},
	}
}

var skillNameSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1}},"required":["name"],"additionalProperties":false}`)

type skillReadTool struct{ skills *SkillsService }

func (t skillReadTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "skill_read",
		Description: "Load one installed skill's full instructions by exact name, after its description in the Available skills list matches the current task",
		Schema:      skillNameSchema,
	}
}

func (t skillReadTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, errors.New("name is required")
	}
	skill, err := t.skills.Show(ctx, input.Name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}{Name: skill.Name, Description: skill.Description, Content: skill.Body})
}

type skillToggleTool struct {
	skills      *SkillsService
	name        string
	description string
	enable      bool
}

func (t skillToggleTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{Name: t.name, Description: t.description, Schema: skillNameSchema}
}

func (t skillToggleTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeStrict(raw, &input); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, errors.New("name is required")
	}
	var err error
	if t.enable {
		err = t.skills.Enable(ctx, input.Name)
	} else {
		err = t.skills.Disable(ctx, input.Name)
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(`{"updated":true}`), nil
}
