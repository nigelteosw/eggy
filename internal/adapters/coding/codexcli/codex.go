package codexcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

const maxLoggedResult = 4096

var ErrRunNotFound = errors.New("coding run not found")

const resultSchema = `{
  "type": "object",
  "properties": {
    "summary": {"type": "string"},
    "validation": {"type": "string"},
    "commit_message": {"type": "string"},
    "changed_files": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["summary", "validation", "commit_message", "changed_files"],
  "additionalProperties": false
}`

type Adapter struct {
	executable string
	runner     ports.Runner
	maxOutput  int64
	home       string
	mu         sync.Mutex
	active     map[string]context.CancelFunc
}

func New(executable string, runner ports.Runner, maxOutput int64, home string) *Adapter {
	if maxOutput <= 0 {
		maxOutput = 1 << 20
	}
	return &Adapter{executable: executable, runner: runner, maxOutput: maxOutput, home: home, active: map[string]context.CancelFunc{}}
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
	schema, err := os.CreateTemp(request.Workspace, ".eggy-codex-result-*.schema.json")
	if err != nil {
		return ports.CodingResult{}, fmt.Errorf("create Codex result schema: %w", err)
	}
	schemaPath := schema.Name()
	defer os.Remove(schemaPath)
	if _, err := schema.WriteString(resultSchema); err != nil {
		schema.Close()
		return ports.CodingResult{}, fmt.Errorf("write Codex result schema: %w", err)
	}
	if err := schema.Close(); err != nil {
		return ports.CodingResult{}, fmt.Errorf("close Codex result schema: %w", err)
	}
	sandbox := "workspace-write"
	if request.ReadOnly {
		sandbox = "read-only"
	}
	command := ports.Command{
		Argv: []string{a.executable, "exec", "--json", "--sandbox", sandbox, "--output-schema", schemaPath, request.Instruction},
		Dir:  request.Workspace, Env: map[string]string{"CODEX_HOME": a.home}, MaxOutput: a.maxOutput,
	}
	var result ports.CommandResult
	err = nil
	if streaming, ok := a.runner.(ports.StreamingRunner); ok {
		result, err = streaming.ExecuteStreaming(runContext, command, func(line string) { emitProgressLine(line, progress) })
	} else {
		result, err = a.runner.Execute(runContext, command)
	}
	if err != nil {
		return ports.CodingResult{}, err
	}
	if _, ok := a.runner.(ports.StreamingRunner); ok {
		return parseJSONL(result.Stdout, nil, !request.ReadOnly)
	}
	return parseJSONL(result.Stdout, progress, !request.ReadOnly)
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

func parseJSONL(output string, progress func(ports.CodingProgress), requireCommitMessage bool) (ports.CodingResult, error) {
	var result ports.CodingResult
	var validations []string
	var finalMessage string
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
			finalMessage = event.Item.Text
			if summary, ok := structuredSummary(event.Item.Text); ok {
				emit(progress, ports.CodingProgress{Kind: "message", Message: summary})
			}
		case event.Type == "error":
			return ports.CodingResult{}, errors.New(event.Message)
		case event.Type == "turn.completed":
			emit(progress, ports.CodingProgress{Kind: "completed", Message: "Codex turn completed"})
		}
	}
	if err := scanner.Err(); err != nil {
		return ports.CodingResult{}, err
	}
	if finalMessage == "" {
		return ports.CodingResult{}, errors.New("Codex produced no final message")
	}
	var structured struct {
		Summary       string   `json:"summary"`
		Validation    string   `json:"validation"`
		CommitMessage string   `json:"commit_message"`
		ChangedFiles  []string `json:"changed_files"`
	}
	if err := json.Unmarshal([]byte(finalMessage), &structured); err != nil {
		if extractErr := json.Unmarshal([]byte(extractStructuredJSON(finalMessage)), &structured); extractErr != nil {
			slog.Default().Error("Codex produced an invalid structured result",
				"error", err, "raw_result", truncate(finalMessage, maxLoggedResult))
			return ports.CodingResult{}, fmt.Errorf("Codex produced an invalid structured result: %w", err)
		}
	}
	if strings.TrimSpace(structured.Summary) == "" {
		return ports.CodingResult{}, errors.New("Codex structured result summary is empty")
	}
	if requireCommitMessage && strings.TrimSpace(structured.CommitMessage) == "" {
		return ports.CodingResult{}, errors.New("Codex structured result commit_message is empty")
	}
	result.Summary = structured.Summary
	result.CommitMessage = structured.CommitMessage
	result.ChangedFiles = append([]string(nil), structured.ChangedFiles...)
	if strings.TrimSpace(structured.Validation) != "" {
		validations = append([]string{strings.TrimSpace(structured.Validation)}, validations...)
	}
	result.Validation = strings.Join(validations, "\n\n")
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
		if summary, ok := structuredSummary(event.Item.Text); ok {
			emit(progress, ports.CodingProgress{Kind: "message", Message: summary})
		}
	case event.Type == "turn.completed":
		emit(progress, ports.CodingProgress{Kind: "completed", Message: "Codex turn completed"})
	case event.Type == "error":
		emit(progress, ports.CodingProgress{Kind: "error", Message: event.Message})
	}
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "...(truncated)"
}

// extractStructuredJSON recovers the result contract's JSON object from output that
// deviates from "pure JSON" despite the schema requiring it, e.g. when the CLI wraps
// the object in a markdown code fence or adds prose before/after it.
func extractStructuredJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if fenced := fencedJSON(trimmed); fenced != "" {
		trimmed = fenced
	}
	if start := strings.Index(trimmed, "{"); start >= 0 {
		if end := strings.LastIndex(trimmed, "}"); end > start {
			trimmed = trimmed[start : end+1]
		}
	}
	return trimmed
}

func fencedJSON(text string) string {
	const fence = "```"
	start := strings.Index(text, fence)
	if start == -1 {
		return ""
	}
	rest := text[start+len(fence):]
	rest = strings.TrimPrefix(rest, "json")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, fence)
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func structuredSummary(value string) (string, bool) {
	var result struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(value), &result); err != nil || strings.TrimSpace(result.Summary) == "" {
		return "", false
	}
	return result.Summary, true
}
