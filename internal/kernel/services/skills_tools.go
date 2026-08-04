package services

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

func NewSkillTools(skills *SkillsService) []ports.Tool {
	return []ports.Tool{skillReadTool{skills: skills}}
}

var skillNameSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1}},"required":["name"],"additionalProperties":false}`)

type skillReadTool struct{ skills *SkillsService }

func (t skillReadTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "skill_read",
		Description: "Load one installed skill's full instructions by exact name, after its description in the Available skills list matches the current task",
		Schema:      skillNameSchema,
		Effect:      ports.ReadOnlyTool(),
	}
}

func (t skillReadTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
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
