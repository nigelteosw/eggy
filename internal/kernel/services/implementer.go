package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/kernel/agent"
	"github.com/nigelteosw/eggy/internal/ports"
)

const implementationSystemPrompt = `Eggy implementation contract
- You are editing a single, already-cloned, already-branched Git checkout. Work only inside it.
- Do not run git commit, git push, git branch, git checkout, or any command that creates, switches, renames, or deletes a branch, or changes HEAD. Eggy performs each of those only after its own independent owner approval.
- Use read_file and terminal to explore the checkout. Use patch to make an exact, minimal edit to an existing file (old_string must match exactly once). Use write_file to create a new file or fully replace one.
- Run this repository's own build/test/lint commands via terminal to validate your change before finishing, and report what you ran in the validation field.
- When the change is complete and validated, call finish_implementation exactly once with a non-empty summary of what changed and why, a validation field describing what you ran and its result, a commit_message suitable for the change, and changed_files listing every file path you modified or created.`

// Implementer runs the bounded, tool-driven implementation loop against an
// already-prepared workspace and returns its structured result.
type Implementer interface {
	Implement(ctx context.Context, runID, workspace, instruction string, progress func(ports.CodingProgress)) (ports.CodingResult, error)
	Interrupt(runID string) error
}

// NativeImplementer drives agent.Loop.RunImplementation with Eggy's own
// file/terminal tools instead of an external CLI subprocess.
type NativeImplementer struct {
	loop     *agent.Loop
	aliasFor func(context.Context) (string, error)

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

func NewNativeImplementer(loop *agent.Loop, aliasFor func(context.Context) (string, error)) *NativeImplementer {
	return &NativeImplementer{loop: loop, aliasFor: aliasFor, active: map[string]context.CancelFunc{}}
}

func (n *NativeImplementer) Implement(ctx context.Context, runID, workspace, instruction string, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	runContext, cancel := context.WithCancel(ctx)
	n.mu.Lock()
	if _, exists := n.active[runID]; exists {
		n.mu.Unlock()
		cancel()
		return ports.CodingResult{}, fmt.Errorf("coding run %q is already active", runID)
	}
	n.active[runID] = cancel
	n.mu.Unlock()
	defer func() {
		cancel()
		n.mu.Lock()
		delete(n.active, runID)
		n.mu.Unlock()
	}()

	alias, err := n.aliasFor(runContext)
	if err != nil {
		return ports.CodingResult{}, err
	}
	runContext = withWorkspace(runContext, workspace)
	messages := []ports.Message{
		{Role: ports.RoleSystem, Content: implementationSystemPrompt},
		{Role: ports.RoleUser, Content: instruction},
	}
	runResult, err := n.loop.RunImplementationWithEvents(runContext, alias, messages, "finish_implementation", func(event agent.ImplementationEvent) {
		if progress == nil {
			return
		}
		if message := implementationProgressMessage(event); message != "" {
			progress(ports.CodingProgress{Kind: "milestone", Message: message, RunID: runID})
		}
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ports.CodingResult{}, err
		}
		return ports.CodingResult{}, fmt.Errorf("implementation run failed: %w", err)
	}
	var structured struct {
		Summary       string   `json:"summary"`
		Validation    string   `json:"validation"`
		CommitMessage string   `json:"commit_message"`
		ChangedFiles  []string `json:"changed_files"`
	}
	if err := json.Unmarshal(runResult.Terminal, &structured); err != nil {
		return ports.CodingResult{}, errors.New("finish_implementation produced an invalid result")
	}
	return ports.CodingResult{Summary: structured.Summary, Validation: structured.Validation, CommitMessage: structured.CommitMessage, ChangedFiles: structured.ChangedFiles}, nil
}

func implementationProgressMessage(event agent.ImplementationEvent) string {
	if event.Kind == "tool_error" {
		return "Blocked: " + event.Call.Name + " failed"
	}
	if event.Kind != "tool_end" {
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
	return ""
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

func (n *NativeImplementer) Interrupt(runID string) error {
	n.mu.Lock()
	cancel, ok := n.active[runID]
	n.mu.Unlock()
	if !ok {
		return fmt.Errorf("coding run %q is not active", runID)
	}
	cancel()
	return nil
}
