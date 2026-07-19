package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

var ErrRunNotFound = errors.New("coding run not found")

type Adapter struct {
	executable string
	runner     ports.Runner
	maxOutput  int64
	mu         sync.Mutex
	active     map[string]context.CancelFunc
}

func New(executable string, runner ports.Runner, maxOutput int64) *Adapter {
	if maxOutput <= 0 {
		maxOutput = 1 << 20
	}
	return &Adapter{executable: executable, runner: runner, maxOutput: maxOutput, active: map[string]context.CancelFunc{}}
}

func (a *Adapter) Run(ctx context.Context, request ports.CodingRequest, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	runContext, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	if _, exists := a.active[request.RunID]; exists {
		a.mu.Unlock()
		cancel()
		return ports.CodingResult{}, errors.New("coding run already active")
	}
	a.active[request.RunID] = cancel
	a.mu.Unlock()
	defer func() { cancel(); a.mu.Lock(); delete(a.active, request.RunID); a.mu.Unlock() }()
	command := ports.Command{
		Argv: []string{a.executable, "exec", "--json", "--sandbox", "workspace-write", request.Instruction},
		Dir:  request.Workspace, Env: request.Environment, MaxOutput: a.maxOutput,
	}
	var result ports.CommandResult
	var err error
	if streaming, ok := a.runner.(ports.StreamingRunner); ok {
		result, err = streaming.ExecuteStreaming(runContext, command, func(line string) { emitProgressLine(line, progress) })
	} else {
		result, err = a.runner.Execute(runContext, command)
	}
	if err != nil {
		return ports.CodingResult{}, err
	}
	if _, ok := a.runner.(ports.StreamingRunner); ok {
		return parseJSONL(result.Stdout, nil)
	}
	return parseJSONL(result.Stdout, progress)
}

func (a *Adapter) Interrupt(runID string) error {
	a.mu.Lock()
	cancel, exists := a.active[runID]
	a.mu.Unlock()
	if !exists {
		return ErrRunNotFound
	}
	cancel()
	return nil
}

func parseJSONL(output string, progress func(ports.CodingProgress)) (ports.CodingResult, error) {
	var result ports.CodingResult
	var validations []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type, Text, Command, AggregatedOutput string
				ExitCode                              int `json:"exit_code"`
			} `json:"item"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			emit(progress, ports.CodingProgress{Kind: "diagnostic", Message: "ignored malformed Codex event"})
			continue
		}
		switch {
		case event.Type == "thread.started":
			emit(progress, ports.CodingProgress{Kind: "started", Message: "Codex run started"})
		case event.Type == "item.completed" && event.Item.Type == "command_execution":
			validation := fmt.Sprintf("%s (exit %d)\n%s", event.Item.Command, event.Item.ExitCode, event.Item.AggregatedOutput)
			validations = append(validations, strings.TrimSpace(validation))
			emit(progress, ports.CodingProgress{Kind: "command", Message: event.Item.Command})
		case event.Type == "item.completed" && event.Item.Type == "agent_message":
			result.Summary = event.Item.Text
			emit(progress, ports.CodingProgress{Kind: "message", Message: event.Item.Text})
		case event.Type == "error":
			return ports.CodingResult{}, errors.New(event.Message)
		case event.Type == "turn.completed":
			emit(progress, ports.CodingProgress{Kind: "completed", Message: "Codex turn completed"})
		}
	}
	if err := scanner.Err(); err != nil {
		return ports.CodingResult{}, err
	}
	if result.Summary == "" {
		return ports.CodingResult{}, errors.New("Codex produced no final message")
	}
	result.Validation = strings.Join(validations, "\n\n")
	result.CommitMessage = firstLine(result.Summary)
	return result, nil
}

func emit(callback func(ports.CodingProgress), progress ports.CodingProgress) {
	if callback != nil {
		callback(progress)
	}
}

func emitProgressLine(line string, progress func(ports.CodingProgress)) {
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Command string `json:"command"`
		} `json:"item"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		emit(progress, ports.CodingProgress{Kind: "diagnostic", Message: "ignored malformed Codex event"})
		return
	}
	switch {
	case event.Type == "thread.started":
		emit(progress, ports.CodingProgress{Kind: "started", Message: "Codex run started"})
	case event.Type == "item.completed" && event.Item.Type == "command_execution":
		emit(progress, ports.CodingProgress{Kind: "command", Message: event.Item.Command})
	case event.Type == "item.completed" && event.Item.Type == "agent_message":
		emit(progress, ports.CodingProgress{Kind: "message", Message: event.Item.Text})
	case event.Type == "turn.completed":
		emit(progress, ports.CodingProgress{Kind: "completed", Message: "Codex turn completed"})
	case event.Type == "error":
		emit(progress, ports.CodingProgress{Kind: "error", Message: event.Message})
	}
}
func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}
