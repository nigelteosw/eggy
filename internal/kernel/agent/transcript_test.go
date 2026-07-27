package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nigelteosw/eggy/internal/ports"
)

type recordingTranscript struct {
	events      []Event
	checkpoints []string
}

func (r *recordingTranscript) Append(_ context.Context, event Event) {
	r.events = append(r.events, event)
}
func (r *recordingTranscript) Checkpoint(_ context.Context, summary string) {
	r.checkpoints = append(r.checkpoints, summary)
}

// Every turn is recorded, not only a turn that happens to be editing: the
// loop writes the transcript itself, so nothing about the caller decides
// whether what happened is durable.
func TestLoopRecordsEveryEventOnTheTranscript(t *testing.T) {
	model := &queuedModel{responses: []ports.ModelResponse{
		{Message: ports.Message{Role: ports.RoleAssistant, ToolCalls: []ports.ToolCall{{ID: "1", Name: "status", Arguments: json.RawMessage(`{}`)}}}},
		{Message: ports.Message{Role: ports.RoleAssistant, Content: "all good"}},
	}}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "status", result: json.RawMessage(`{"ok":true}`)},
	}, ContextPolicy{})
	transcript := &recordingTranscript{}

	if _, err := loop.Run(context.Background(), "model", "", "how are things", nil, RunOptions{Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(transcript.events))
	for _, event := range transcript.events {
		kinds = append(kinds, event.Kind)
	}
	want := []string{EventAssistantMessage, EventToolStart, EventToolEnd}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("transcript kinds=%v, want %v", kinds, want)
	}
	if len(transcript.checkpoints) != 0 {
		t.Fatalf("a short turn needs no checkpoint: %v", transcript.checkpoints)
	}
}

// A long turn compacts and continues. The step count is a checkpoint
// trigger, not a work cap: the turn still ends only when the model stops
// calling tools.
func TestLoopCompactsInsteadOfEndingALongTurn(t *testing.T) {
	const steps = 6
	responses := make([]ports.ModelResponse, 0, steps+1)
	for i := 0; i < steps; i++ {
		responses = append(responses, ports.ModelResponse{Message: ports.Message{
			Role:      ports.RoleAssistant,
			ToolCalls: []ports.ToolCall{{ID: "call", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}},
		}})
	}
	responses = append(responses, ports.ModelResponse{Message: ports.Message{Role: ports.RoleAssistant, Content: "done"}})
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "read_file", result: json.RawMessage(`{"content":"package main"}`)},
	}, ContextPolicy{RecentSteps: 2})
	transcript := &recordingTranscript{}

	history := []ports.Message{{Role: ports.RoleSystem, Content: "you are Eggy"}}
	result, err := loop.Run(context.Background(), "model", "", "review the package", history, RunOptions{Transcript: transcript})
	if err != nil || result.Message.Content != "done" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(transcript.checkpoints) == 0 {
		t.Fatal("a turn past its context budget must checkpoint")
	}
	last := model.requests[len(model.requests)-1].Messages
	// Instructions and the owner's actual request survive compaction; they
	// are the last thing a long turn should lose.
	if last[0].Content != "you are Eggy" || last[1].Content != "review the package" {
		t.Fatalf("preserved head=%#v", last[:2])
	}
	if !strings.Contains(last[2].Content, "Checkpoint") || last[2].Role != ports.RoleSystem {
		t.Fatalf("checkpoint message=%#v", last[2])
	}
	// Compaction drops whole steps: no tool result may survive without the
	// assistant message whose call it answers, or a provider rejects it.
	live := map[string]bool{}
	for _, message := range last {
		for _, call := range message.ToolCalls {
			live[call.ID] = true
		}
		if message.Role == ports.RoleTool && !live[message.ToolCallID] {
			t.Fatalf("orphaned tool result in the live window: %#v", last)
		}
	}
	// Uncompacted, the window would be the head, the checkpoint, and all
	// six assistant/tool pairs; RecentSteps: 2 keeps only the newest.
	if len(last) != 2+1+2*2 {
		t.Fatalf("live window=%d messages, want the head, a checkpoint, and the two newest steps: %#v", len(last), last)
	}
}

// The runaway guard is the only step-based stop left, and it is a
// malfunction signal rather than "this turn did a lot of work".
func TestLoopStopsARunawayTurnAtTheHardStepLimit(t *testing.T) {
	responses := make([]ports.ModelResponse, 0, 8)
	for i := 0; i < 8; i++ {
		responses = append(responses, ports.ModelResponse{Message: ports.Message{
			Role:      ports.RoleAssistant,
			ToolCalls: []ports.ToolCall{{ID: "call", Name: "status", Arguments: json.RawMessage(`{}`)}},
		}})
	}
	model := &queuedModel{responses: responses}
	loop := NewSelectedLoop(map[string]ModelTarget{"model": {Model: model, ModelID: "id"}}, []ports.Tool{
		&fakeTool{name: "status", result: json.RawMessage(`{}`)},
	}, ContextPolicy{MaxSteps: 3})

	if _, err := loop.Run(context.Background(), "model", "", "spin", nil, RunOptions{}); err != ErrToolStepLimit {
		t.Fatalf("err=%v, want ErrToolStepLimit", err)
	}
}
