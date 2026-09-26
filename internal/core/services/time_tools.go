package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nigelteosw/eggy/internal/ports"
)

// NewCurrentTimeTool gives the model a clock it can trust. A model's own sense
// of "now" comes from its training data, so every relative date -- tomorrow,
// next Friday, in an hour -- is wrong without this, and wrong in a way that
// looks confident.
func NewCurrentTimeTool(now func() time.Time, location *time.Location, timezone string) ports.Tool {
	return currentTimeTool{now: now, location: location, timezone: timezone}
}

type currentTimeTool struct {
	now      func() time.Time
	location *time.Location
	timezone string
}

func (t currentTimeTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "current_time",
		Description: "Refresh the trusted current time and owner timezone during a long-running turn. The turn-start time is already in the system context, so do not call this merely to answer with the current date or time",
		Schema:      json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Effect:      ports.ReadOnlyTool(),
	}
}

func (t currentTimeTool) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := DecodeToolInput(raw, &struct{}{}); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{
		"current_time": t.now().In(t.location).Format(time.RFC3339),
		"timezone":     t.timezone,
	})
}
