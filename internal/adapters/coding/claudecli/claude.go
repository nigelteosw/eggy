package claudecli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

var ErrRunNotFound = errors.New("coding run not found")

const maxProgressMessage = 512

type Adapter struct {
	executable string
	runner     ports.Runner
	maxOutput  int64
	oauthToken string
	configDir  string
	mu         sync.Mutex
	active     map[string]context.CancelFunc
}

func New(executable string, runner ports.Runner, maxOutput int64, oauthToken, configDir string) *Adapter {
	if maxOutput <= 0 {
		maxOutput = 1 << 20
	}
	return &Adapter{
		executable: executable,
		runner:     runner,
		maxOutput:  maxOutput,
		oauthToken: oauthToken,
		configDir:  configDir,
		active:     map[string]context.CancelFunc{},
	}
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
	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.active, request.RunID)
		a.mu.Unlock()
	}()

	permissionMode := "bypassPermissions"
	if request.ReadOnly {
		permissionMode = "plan"
	}
	command := ports.Command{
		Argv: []string{
			a.executable,
			"-p",
			"--output-format", "stream-json",
			"--verbose",
			"--permission-mode", permissionMode,
			request.Instruction,
		},
		Dir: request.Workspace,
		Env: map[string]string{
			"CLAUDE_CODE_OAUTH_TOKEN": a.oauthToken,
			"CLAUDE_CONFIG_DIR":       a.configDir,
		},
		MaxOutput: a.maxOutput,
	}

	var result ports.CommandResult
	var err error
	if streaming, ok := a.runner.(ports.StreamingRunner); ok {
		result, err = streaming.ExecuteStreaming(runContext, command, func(line string) {
			a.emitProgressLine(line, progress)
		})
	} else {
		result, err = a.runner.Execute(runContext, command)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ports.CodingResult{}, err
		}
		return ports.CodingResult{}, errors.New("Claude Code execution failed")
	}
	if _, ok := a.runner.(ports.StreamingRunner); ok {
		return a.parseJSONL(result.Stdout, nil, !request.ReadOnly)
	}
	return a.parseJSONL(result.Stdout, progress, !request.ReadOnly)
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

type streamEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Result  string          `json:"result"`
	Message json.RawMessage `json:"message"`
}

type assistantMessage struct {
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

func (a *Adapter) parseJSONL(output string, progress func(ports.CodingProgress), requireCommitMessage bool) (ports.CodingResult, error) {
	var finalResult string
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var event streamEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			a.emit(progress, "diagnostic", "ignored malformed Claude Code event")
			continue
		}
		switch {
		case event.Type == "error" || event.Type == "system" && event.Subtype == "error":
			return ports.CodingResult{}, errors.New("Claude Code reported an error")
		case event.Type == "result" && event.Subtype != "success" && event.Subtype != "":
			return ports.CodingResult{}, errors.New("Claude Code run failed")
		case event.Type == "result":
			finalResult = event.Result
		}
		a.emitEvent(event, progress)
	}
	if err := scanner.Err(); err != nil {
		return ports.CodingResult{}, errors.New("read Claude Code output")
	}
	if strings.TrimSpace(finalResult) == "" {
		return ports.CodingResult{}, errors.New("Claude Code produced no final result")
	}
	var structured struct {
		Summary       string   `json:"summary"`
		Validation    string   `json:"validation"`
		CommitMessage string   `json:"commit_message"`
		ChangedFiles  []string `json:"changed_files"`
	}
	if err := json.Unmarshal([]byte(finalResult), &structured); err != nil {
		return ports.CodingResult{}, errors.New("Claude Code produced an invalid structured result")
	}
	if strings.TrimSpace(structured.Summary) == "" {
		return ports.CodingResult{}, errors.New("Claude Code structured result summary is empty")
	}
	if requireCommitMessage && strings.TrimSpace(structured.CommitMessage) == "" {
		return ports.CodingResult{}, errors.New("Claude Code structured result commit_message is empty")
	}
	return ports.CodingResult{
		Summary:       structured.Summary,
		Validation:    structured.Validation,
		CommitMessage: structured.CommitMessage,
		ChangedFiles:  append([]string(nil), structured.ChangedFiles...),
	}, nil
}

func (a *Adapter) emitProgressLine(line string, progress func(ports.CodingProgress)) {
	var event streamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		a.emit(progress, "diagnostic", "ignored malformed Claude Code event")
		return
	}
	a.emitEvent(event, progress)
}

func (a *Adapter) emitEvent(event streamEvent, progress func(ports.CodingProgress)) {
	switch {
	case event.Type == "system" && event.Subtype == "init":
		a.emit(progress, "started", "Claude Code run started")
	case event.Type == "system" && strings.Contains(event.Subtype, "retry"):
		a.emit(progress, "diagnostic", "Claude Code retrying request")
	case event.Type == "assistant":
		var message assistantMessage
		if err := json.Unmarshal(event.Message, &message); err != nil {
			a.emit(progress, "diagnostic", "ignored malformed Claude Code assistant event")
			return
		}
		for _, block := range message.Content {
			if block.Type == "tool_use" {
				a.emit(progress, "tool", "Claude Code used "+block.Name)
			}
		}
	case event.Type == "result":
		a.emit(progress, "completed", "Claude Code run completed")
	case event.Type == "error" || event.Type == "system" && event.Subtype == "error":
		a.emit(progress, "error", "Claude Code reported an error")
	}
}

func (a *Adapter) emit(callback func(ports.CodingProgress), kind, message string) {
	if callback == nil {
		return
	}
	if a.oauthToken != "" {
		message = strings.ReplaceAll(message, a.oauthToken, "[redacted]")
	}
	if len(message) > maxProgressMessage {
		message = message[:maxProgressMessage]
	}
	callback(ports.CodingProgress{Kind: kind, Message: message})
}
