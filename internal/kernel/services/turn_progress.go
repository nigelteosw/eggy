package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

// TurnSessionEvent converts one loop event into the durable session event
// shape. It is unchanged from the implementation run's rendering: the
// milestones an owner reads (`Inspected:`, `Edited:`, `Validation:`) are the
// streaming surface, and collapsing two loops into one did not change what
// is worth saying about a step.
func TurnSessionEvent(event agent.Event) ports.ImplementationSessionEvent {
	result := ports.ImplementationSessionEvent{ToolName: event.Call.Name, Content: event.Output, ModelMessage: event.Message}
	switch event.Kind {
	case agent.EventAssistantMessage:
		result.Kind = ports.SessionAssistantMessage
	case agent.EventToolStart:
		result.Kind = ports.SessionToolStart
	case agent.EventToolEnd:
		result.Kind, result.Message = ports.SessionToolResult, TurnProgressMessage(event)
	case agent.EventToolError:
		result.Kind, result.Message = ports.SessionToolError, TurnProgressMessage(event)
	default:
		result.Kind = event.Kind
	}
	return result
}

// TurnProgressMessage renders the owner-facing milestone for one loop event,
// or "" when the event is not worth reporting.
func TurnProgressMessage(event agent.Event) string {
	if event.Kind == agent.EventToolError {
		return "Blocked: " + event.Call.Name + " failed — " + toolErrorMessage(event)
	}
	if event.Kind != agent.EventToolEnd {
		return ""
	}
	path := toolArgument(event.Call.Arguments, "path")
	switch event.Call.Name {
	case "read_file":
		if path != "" {
			return "Inspected: " + path
		}
	case "patch", "write_file":
		if path != "" {
			return "Edited: " + path
		}
	case "propose_change":
		return "Proposed the change for review"
	case "terminal":
		command := toolArgument(event.Call.Arguments, "command")
		if command == "" {
			return "Ran a repository command"
		}
		exitCode := terminalExitCode(event.Output)
		if strings.Contains(command, "test") || strings.Contains(command, "vet") || strings.Contains(command, "build") || strings.Contains(command, "lint") {
			if exitCode != 0 {
				return fmt.Sprintf("Validation: %s failed (exit %d)", command, exitCode)
			}
			return "Validation: " + command + " passed"
		}
		if exitCode != 0 {
			return fmt.Sprintf("Command failed (exit %d): %s", exitCode, command)
		}
		return "Ran: " + command
	}
	return "Called: " + event.Call.Name
}

// toolErrorMessage recovers the human-readable failure reason for a
// tool-error event, preferring the original error and falling back to the
// {"error": ...} payload the loop wrote into the tool message when only the
// event's marshaled output survived (e.g. across a replayed session).
func toolErrorMessage(event agent.Event) string {
	if event.Err != nil {
		return event.Err.Error()
	}
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(event.Output), &payload) == nil && payload.Error != "" {
		return payload.Error
	}
	return "unknown error"
}

func toolArgument(raw json.RawMessage, name string) string {
	var arguments map[string]string
	if json.Unmarshal(raw, &arguments) != nil {
		return ""
	}
	return arguments[name]
}

func terminalExitCode(raw string) int {
	var result struct {
		ExitCode int `json:"exit_code"`
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return result.ExitCode
}
