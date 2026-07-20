package services

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nigelteosw/eggy/internal/ports"
)

const terminalDescription = "Run a shell command (e.g. grep, ls, find, git status, git log, go test) in the repository checkout. Output is captured and bounded; the command runs with restricted environment and a timeout."
const terminalSchema = `{"type":"object","properties":{"command":{"type":"string","minLength":1}},"required":["command"],"additionalProperties":false}`

func runTerminal(ctx context.Context, runner ports.Runner, workspace, command string) (json.RawMessage, error) {
	if runner == nil {
		return nil, errors.New("terminal is unavailable")
	}
	result, err := runner.Execute(ctx, ports.Command{Argv: []string{"sh", "-c", command}, Dir: workspace})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode, "output_truncated": result.OutputTruncated,
	})
}
