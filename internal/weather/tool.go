package weather

import (
	"context"
	"encoding/json"

	"github.com/nigelteosw/eggy/internal/ports"
)

// ToolName is the model-facing name, which the unprompted-turn allowlist
// names too.
const ToolName = "weather"

// NewTool is the model's view of the same shared report the home page shows,
// so Eggy can say "it will rain this afternoon, take an umbrella" from the
// numbers the owner can see on Home.
//
// It takes no arguments on purpose: the area is the deployment's, set in
// settings, and a call carries nothing a model chose to the outside. That is
// what makes it safe on an unprompted turn, where a morning check-in is the
// most useful place to warn about rain.
func NewTool(service *Service) ports.Tool { return tool{service: service} }

type tool struct{ service *Service }

func (tool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:   ToolName,
		Effect: ports.ReadOnlyTool(),
		Description: "Live Singapore weather from data.gov.sg for the deployment's configured area: the current temperature and humidity from the nearest station, " +
			"the next-two-hours forecast, today's low/high, the forecast by period (morning, afternoon, night), and the four-day outlook. " +
			"Use it before advising on rain, umbrellas, heat or outdoor plans. Times are Singapore time.",
		Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func (t tool) Execute(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	report, err := t.service.Report(ctx, false)
	if err != nil {
		return nil, err
	}
	return json.Marshal(report)
}
