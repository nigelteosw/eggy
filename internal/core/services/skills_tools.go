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

var skillReadSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1},"file":{"type":"string","minLength":1}},"required":["name"],"additionalProperties":false}`)

type skillReadTool struct{ skills *SkillsService }

func (t skillReadTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "skill_read",
		Description: "Load one installed skill's full instructions by exact name, after its description in the Available skills list matches the current task; pass file to read one of its reference files",
		Schema:      skillReadSchema,
		Effect:      ports.ReadOnlyTool(),
	}
}

func (t skillReadTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Name string `json:"name"`
		File string `json:"file"`
	}
	if err := DecodeToolInput(raw, &input); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, errors.New("name is required")
	}
	if input.File != "" {
		content, err := t.skills.ReadFile(ctx, input.Name, input.File)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Name    string `json:"name"`
			File    string `json:"file"`
			Content string `json:"content"`
		}{Name: input.Name, File: input.File, Content: content})
	}
	skill, err := t.skills.Show(ctx, input.Name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Content     string   `json:"content"`
		Files       []string `json:"files,omitempty"`
	}{Name: skill.Name, Description: skill.Description, Content: skill.Body, Files: skill.Files})
}
