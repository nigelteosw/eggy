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
		codeTool{def: def("read", "Read lines of a file in the workspace.", `{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":2000}},"required":["path"],"additionalProperties":false}`, ports.ReadOnlyTool()),
			run: func(_ context.Context, in json.RawMessage) (string, error) { return readTool(ws, in) }},
		codeTool{def: def("search", "Search workspace files for a regular expression.", `{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"],"additionalProperties":false}`, ports.ReadOnlyTool()),
			run: func(_ context.Context, in json.RawMessage) (string, error) { return searchTool(ws, in) }},
		codeTool{def: def("edit", "Replace one exact occurrence of old with new in a file, or create a file when old is empty.", `{"type":"object","properties":{"path":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"}},"required":["path","old","new"],"additionalProperties":false}`, ports.ToolEffect{}),
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
	limit := args.Limit
	if limit <= 0 || limit > maxReadLines {
		limit = maxReadLines
	}
	start := min(max(args.Offset, 0), len(lines))
	end := min(start+limit, len(lines))
	return strings.Join(lines[start:end], "\n"), nil
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

func editTool(ws string, in json.RawMessage) (string, error) {
	var args struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	path, err := resolveInside(ws, args.Path)
	if err != nil {
		return "", err
	}
	if args.Old == "" {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("%s already exists; give old text to edit it", args.Path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(args.New), 0o600); err != nil {
			return "", err
		}
		return "created " + args.Path, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	count := strings.Count(string(body), args.Old)
	if count != 1 {
		return "", fmt.Errorf("old text matches %d times in %s; it must match exactly once", count, args.Path)
	}
	updated := strings.Replace(string(body), args.Old, args.New, 1)
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return "", err
	}
	return "edited " + args.Path, nil
}

func bashTool(ctx context.Context, runner CommandRunner, runDir string, in json.RawMessage) (string, error) {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return "", err
	}
	result, err := runner.Run(ctx, SandboxSpec{RunDir: runDir, Argv: []string{"bash", "-c", args.Command}})
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
	result, err := runner.Run(ctx, SandboxSpec{RunDir: runDir, Argv: argv})
	if err != nil {
		return "", err
	}
	run.mu.Lock()
	run.CheckResults = append(run.CheckResults, CheckResult{Argv: argv, ExitCode: result.ExitCode, TimedOut: result.TimedOut})
	run.mu.Unlock()
	return formatResult(result), nil
}

// formatResult is what the model sees of a command: its output, then how it ended.
func formatResult(result SandboxResult) string {
	status := fmt.Sprintf("exit %d", result.ExitCode)
	if result.TimedOut {
		status = "timed out"
	}
	if result.Truncated {
		status += " (output truncated)"
	}
	return result.Output + "\n[" + status + "]"
}
