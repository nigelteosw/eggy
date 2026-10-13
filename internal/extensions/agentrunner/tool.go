package agentrunner

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

// agentRunnerTool is the owner-facing tool. It has no start action on purpose:
// a run starts only from a trusted ingress, never from a model's tool call.
type agentRunnerTool struct {
	s *Service
}

// NewTool returns the agent_runner tool. Status and list only read; cancel and
// answer change a run, which is what the gate asks about.
func NewTool(s *Service) ports.Tool {
	return agentRunnerTool{s: s}
}

func (agentRunnerTool) Definition() ports.ToolDefinition {
	return ports.ToolDefinition{
		Name:        "agent_runner",
		Description: "Check on the agent runner's coding runs: status or list them, read a run's steps (what it did and why it stopped), cancel one, or answer its question. It cannot start a run; the owner starts runs from the Agent Runner page or with /code run.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["status","list","steps","cancel","answer"]},"id":{"type":"string"},"text":{"type":"string"}},"required":["action"],"additionalProperties":false}`),
		Effect:      ports.ToolEffect{Mutations: []string{"cancel", "answer"}},
	}
}

// runView is what the tool reports about a run. It omits the brief and the
// model's raw output, so an owner turn reads a short status, not a transcript.
type runView struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	State      string `json:"state"`
	Question   string `json:"question,omitempty"`
	PRURL      string `json:"pr_url,omitempty"`
	Failure    string `json:"failure,omitempty"`
}

func viewOf(run ports.AgentRun) runView {
	return runView{ID: run.ID, Repository: run.Repository, State: string(run.State), Question: run.Question, PRURL: run.PRURL, Failure: run.Failure}
}

func (t agentRunnerTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, err
	}
	var result any
	switch args.Action {
	case "status":
		run, err := t.s.Get(ctx, args.ID)
		if err != nil {
			return nil, err
		}
		result = viewOf(run)
	case "list":
		runs, err := t.s.List(ctx)
		if err != nil {
			return nil, err
		}
		views := make([]runView, 0, len(runs))
		for _, run := range runs {
			views = append(views, viewOf(run))
		}
		result = views
	case "steps":
		steps, err := t.s.Steps(ctx, args.ID)
		if err != nil {
			return nil, err
		}
		if steps == nil {
			steps = []ports.AgentRunEvent{}
		}
		result = steps
	case "cancel":
		run, err := t.s.Cancel(ctx, args.ID)
		if err != nil {
			return nil, err
		}
		result = viewOf(run)
	case "answer":
		run, err := t.s.Answer(ctx, args.ID, args.Text)
		if err != nil {
			return nil, err
		}
		result = viewOf(run)
	default:
		return nil, errors.New("action must be status, list, steps, cancel or answer")
	}
	return json.Marshal(result)
}
