package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nigelteosw/eggy/internal/ports"
)

// Limits on what one tool call returns to the model.
const (
	maxListEntries  = 500
	maxReadLines    = 2000
	maxSearchResult = 200
	// maxReadBytes bounds one read's text. It stays under the core's bound on a
	// tool result, so a read is never cut from its start.
	maxReadBytes = 40 << 10
)

// ActiveRun is the per-run state the coding tools share: the repository's
// checks, and the exit codes of every check the runner actually executed.
// Only CheckResults count as evidence in a pull request.
type ActiveRun struct {
	ID           string
	Checks       [][]string
	mu           sync.Mutex
	CheckResults []CheckResult
	question     string
}

// setQuestion records the question the model asked the owner. The worker
// reads it when the turn ends, which is how ask_owner ends a run's work.
func (r *ActiveRun) setQuestion(q string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.question = q
}

// results returns a copy of the check results recorded so far.
func (r *ActiveRun) results() []CheckResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.CheckResults)
}

// Question returns the question asked this turn, or "".
func (r *ActiveRun) Question() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.question
}

// CheckResult is one check the runner executed, with its real exit code.
type CheckResult struct {
	Argv     []string
	ExitCode int
	TimedOut bool
}

// codeTool is one coding tool: a definition and a function over its input.
type codeTool struct {
	def ports.ToolDefinition
	run func(context.Context, json.RawMessage) (string, error)
}

func (t codeTool) Definition() ports.ToolDefinition { return t.def }

func (t codeTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	out, err := t.run(ctx, input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"output": out})
}

// CodingTools returns the coding tools for one run. bash is offered only when
// withBash is true, which the bootstrap sets only after a passing sandbox probe.
func CodingTools(w *Workspaces, run *ActiveRun, withBash bool) []ports.Tool {
	ws := filepath.Join(w.Dir(run.ID), "ws")
	runDir := w.Dir(run.ID)
	tools := []ports.Tool{
		codeTool{def: def("list", "List the entries of a directory in the workspace.", `{"type":"object","properties":{"path":{"type":"string"}},"additionalProperties":false}`, ports.ReadOnlyTool()),
			run: func(_ context.Context, in json.RawMessage) (string, error) { return listTool(ws, in) }},
		codeTool{def: def("read", "Read lines of a file in the workspace.", `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0,"description":"1-based line to start at"},"limit":{"type":"integer","minimum":1,"maximum":2000}},"required":["path"],"additionalProperties":false}`, ports.ReadOnlyTool()),
			run: func(_ context.Context, in json.RawMessage) (string, error) { return readTool(ws, in) }},
		codeTool{def: def("search", "Search workspace files for a regular expression.", `{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`, ports.ReadOnlyTool()),
			run: func(_ context.Context, in json.RawMessage) (string, error) { return searchTool(ws, in) }},
		codeTool{def: def("edit", "Replace exact text in a file. Each edits[].old must match exactly once in the file as it was before this call, and edits must not overlap: merge nearby changes into one edit. An empty old creates a file that does not exist.", `{"type":"object","properties":{"path":{"type":"string"},"edits":{"type":"array","minItems":1,"items":{"type":"object","properties":{"old":{"type":"string"},"new":{"type":"string"}},"required":["old","new"],"additionalProperties":false}}},"required":["path","edits"],"additionalProperties":false}`, ports.ToolEffect{}),
			run: func(_ context.Context, in json.RawMessage) (string, error) { return editTool(ws, in) }},
		codeTool{def: def("run_check", "Run one of the repository's configured checks by index. Its exit code is recorded.", `{"type":"object","properties":{"index":{"type":"integer","minimum":0}},"required":["index"],"additionalProperties":false}`, ports.ToolEffect{}),
			run: func(ctx context.Context, in json.RawMessage) (string, error) {
				return runCheckTool(ctx, w.runner, runDir, run, in)
			}},
	}
	if withBash {
		tools = append(tools, codeTool{def: def("bash", "Run a shell command in the workspace inside the sandbox. Its exit code is not a check result.", `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`, ports.ToolEffect{}),
			run: func(ctx context.Context, in json.RawMessage) (string, error) {
				return bashTool(ctx, w.runner, runDir, in)
			}})
	}
	return tools
}

func def(name, description, schema string, effect ports.ToolEffect) ports.ToolDefinition {
	return ports.ToolDefinition{Name: name, Description: description, Schema: json.RawMessage(schema), Effect: effect}
}

func listTool(ws string, in json.RawMessage) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	dir, err := resolveInside(ws, args.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > maxListEntries {
		names = append(names[:maxListEntries], fmt.Sprintf("... %d more", len(names)-maxListEntries))
	}
	return strings.Join(names, "\n"), nil
}

// readTool returns lines of a file, from a 1-based offset. A read that stops
// short says where to continue, so the model never mistakes a cut for the end.
func readTool(ws string, in json.RawMessage) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	path, err := resolveInside(ws, args.Path)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(body), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	limit := args.Limit
	if limit <= 0 || limit > maxReadLines {
		limit = maxReadLines
	}
	start := max(args.Offset-1, 0)
	if start >= len(lines) && len(lines) > 0 {
		return "", fmt.Errorf("offset %d is beyond the end of the file (%d lines)", args.Offset, len(lines))
	}
	end := min(start+limit, len(lines))
	var shown []string
	size := 0
	for i := start; i < end; i++ {
		if size+len(lines[i])+1 > maxReadBytes {
			if len(shown) == 0 {
				return fmt.Sprintf("[line %d is %d bytes, over the %d-byte read limit; read it with: sed -n '%dp' %s | head -c %d]",
					i+1, len(lines[i]), maxReadBytes, i+1, args.Path, maxReadBytes), nil
			}
			break
		}
		shown = append(shown, lines[i])
		size += len(lines[i]) + 1
	}
	text := strings.Join(shown, "\n")
	if next := start + len(shown); next < len(lines) {
		text += fmt.Sprintf("\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", start+1, next, len(lines), next+1)
	}
	return text, nil
}

func searchTool(ws string, in json.RawMessage) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return "", fmt.Errorf("pattern: %w", err)
	}
	root, err := resolveInside(ws, args.Path)
	if err != nil {
		return "", err
	}
	var matches []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.Type().IsRegular() || len(matches) >= maxSearchResult {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(ws, path)
		for i, line := range strings.Split(string(body), "\n") {
			if re.MatchString(line) && len(matches) < maxSearchResult {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, i+1, line))
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return strings.Join(matches, "\n"), nil
}

func bashTool(ctx context.Context, runner CommandRunner, runDir string, in json.RawMessage) (string, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	result, err := runner.Run(ctx, SandboxSpec{RunDir: runDir, Argv: []string{"bash", "-c", args.Command}, FullOutputDir: filepath.Join(runDir, "tmp")})
	if err != nil {
		return "", err
	}
	return formatResult(result), nil
}

func runCheckTool(ctx context.Context, runner CommandRunner, runDir string, run *ActiveRun, in json.RawMessage) (string, error) {
	var args struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	if args.Index < 0 || args.Index >= len(run.Checks) {
		return "", fmt.Errorf("index %d is not a configured check (have %d)", args.Index, len(run.Checks))
	}
	argv := run.Checks[args.Index]
	result, err := runner.Run(ctx, SandboxSpec{RunDir: runDir, Argv: argv, FullOutputDir: filepath.Join(runDir, "tmp")})
	if err != nil {
		return "", err
	}
	run.mu.Lock()
	run.CheckResults = append(run.CheckResults, CheckResult{Argv: argv, ExitCode: result.ExitCode, TimedOut: result.TimedOut})
	run.mu.Unlock()
	return formatResult(result), nil
}

// formatResult is what the model sees of a command: its output, then how it
// ended. Output that was cut says what it kept, and where the whole of it is.
func formatResult(result SandboxResult) string {
	status := fmt.Sprintf("exit %d", result.ExitCode)
	if result.TimedOut {
		status = "timed out"
	}
	output := result.Output
	if result.Truncated {
		note := fmt.Sprintf("[Output was cut: showing the last %d bytes.", len(output))
		if result.FullOutputPath != "" {
			note += " The whole output is in " + result.FullOutputPath + "; search it with a narrower command."
		}
		if result.FullOutputIncomplete {
			note += " That copy is incomplete."
		}
		output = note + "]\n" + output
	}
	return output + "\n[" + status + "]"
}
