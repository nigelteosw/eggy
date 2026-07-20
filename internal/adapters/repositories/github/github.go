package github

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nigelteosw/eggy/internal/ports"
)

type Adapter struct {
	runner  ports.Runner
	token   string
	apiBase string
	http    *http.Client
}

var _ ports.CodingRepository = (*Adapter)(nil)
var _ ports.RepositoryCommitter = (*Adapter)(nil)
var _ ports.RepositoryPusher = (*Adapter)(nil)
var _ ports.PullRequestProvider = (*Adapter)(nil)
var _ ports.RepositoryCapabilityProvider = (*Adapter)(nil)
var _ ports.RepositoryReader = (*Adapter)(nil)

var ErrDiffTooLarge = errors.New("repository diff exceeds configured output limit")

var errStopWalk = errors.New("stop walk")

const (
	maxScannedFileBytes = 1 << 20
	maxSearchLineLength = 300
)

func New(runner ports.Runner, token, apiBase string, client *http.Client) *Adapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &Adapter{runner: runner, token: token, apiBase: strings.TrimRight(apiBase, "/"), http: client}
}

func (a *Adapter) RepositoryCapabilities() ports.RepositoryCapabilities {
	writeReady := strings.TrimSpace(a.token) != ""
	return ports.RepositoryCapabilities{Commit: true, Push: writeReady, PullRequest: writeReady}
}

func (a *Adapter) Clone(ctx context.Context, repository ports.Repository, workspace string) error {
	if a.runner == nil {
		return errors.New("repository runner is unavailable")
	}
	cleanup, environment, err := a.askpass(filepath.Dir(workspace))
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = a.runner.Execute(ctx, ports.Command{
		Argv: []string{"git", "clone", "--branch", repository.BaseBranch, "--single-branch", "--", repository.CloneURL, workspace},
		Dir:  filepath.Dir(workspace), Env: environment,
	})
	return err
}

func (a *Adapter) Inspect(ctx context.Context, workspace string) (string, error) {
	data, err := os.ReadFile(filepath.Join(workspace, "AGENTS.md"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *Adapter) CreateBranch(ctx context.Context, workspace, branch string) error {
	if !validBranch(branch) {
		return errors.New("invalid branch")
	}
	_, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "checkout", "-b", branch}, Dir: workspace})
	return err
}

func (a *Adapter) Head(ctx context.Context, workspace string) (string, error) {
	result, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "rev-parse", "HEAD"}, Dir: workspace})
	if err != nil {
		return "", err
	}
	if result.OutputTruncated {
		return "", errors.New("git head output was truncated")
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (a *Adapter) WorkspaceRevision(ctx context.Context, workspace string) (ports.WorkspaceRevision, error) {
	result, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "symbolic-ref", "--quiet", "--short", "HEAD"}, Dir: workspace})
	if err != nil {
		return ports.WorkspaceRevision{}, fmt.Errorf("read current branch: %w", err)
	}
	if result.OutputTruncated {
		return ports.WorkspaceRevision{}, errors.New("git branch output was truncated")
	}
	head, err := a.Head(ctx, workspace)
	if err != nil {
		return ports.WorkspaceRevision{}, err
	}
	return ports.WorkspaceRevision{Branch: strings.TrimSpace(result.Stdout), Head: head}, nil
}

func (a *Adapter) RemoteHead(ctx context.Context, workspace, branch string) (string, error) {
	if !validBranch(branch) {
		return "", errors.New("invalid remote branch")
	}
	cleanup, environment, err := a.askpass(filepath.Dir(workspace))
	if err != nil {
		return "", err
	}
	defer cleanup()
	result, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "ls-remote", "origin", "refs/heads/" + branch}, Dir: workspace, Env: environment})
	if err != nil {
		return "", err
	}
	if result.OutputTruncated {
		return "", errors.New("remote head output was truncated")
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) == 0 {
		return "", errors.New("remote branch does not exist")
	}
	return fields[0], nil
}

func (a *Adapter) CheckRemote(ctx context.Context, repository ports.Repository, workspace string) error {
	if a.runner == nil {
		return errors.New("repository runner is unavailable")
	}
	if !validBranch(repository.BaseBranch) {
		return errors.New("invalid base branch")
	}
	cleanup, environment, err := a.askpass(filepath.Dir(workspace))
	if err != nil {
		return err
	}
	defer cleanup()
	result, err := a.runner.Execute(ctx, ports.Command{
		Argv: []string{"git", "ls-remote", "--exit-code", "--heads", repository.CloneURL, repository.BaseBranch},
		Dir:  workspace, Env: environment,
	})
	if err != nil {
		return fmt.Errorf("repository is not reachable: %w", err)
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return fmt.Errorf("base branch %q not found in %q", repository.BaseBranch, repository.Name)
	}
	return nil
}

func (a *Adapter) Diff(ctx context.Context, workspace string) (string, error) {
	if _, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "add", "-A"}, Dir: workspace}); err != nil {
		return "", err
	}
	result, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "diff", "--cached", "--no-ext-diff", "--binary", "HEAD"}, Dir: workspace})
	if err != nil {
		return "", err
	}
	if result.OutputTruncated {
		return "", ErrDiffTooLarge
	}
	return result.Stdout, nil
}

func (a *Adapter) Status(ctx context.Context, workspace string) (string, error) {
	result, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "status", "--porcelain=v1", "--branch"}, Dir: workspace})
	if err != nil {
		return "", err
	}
	if result.OutputTruncated {
		return "", errors.New("git status output was truncated")
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (a *Adapter) Branches(ctx context.Context, workspace string) ([]string, error) {
	result, err := a.runner.Execute(ctx, ports.Command{
		Argv: []string{"git", "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes"}, Dir: workspace,
	})
	if err != nil {
		return nil, err
	}
	if result.OutputTruncated {
		return nil, errors.New("git branch output was truncated")
	}
	var branches []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

func (a *Adapter) ListTree(ctx context.Context, workspace, path string, maxEntries int) ([]ports.WorkspaceEntry, error) {
	root, err := safeWorkspacePath(workspace, path)
	if err != nil {
		return nil, err
	}
	if maxEntries <= 0 {
		maxEntries = 200
	}
	var entries []ports.WorkspaceEntry
	walkErr := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == root {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(workspace, current)
		if err != nil {
			return err
		}
		entries = append(entries, ports.WorkspaceEntry{Path: relative, IsDir: entry.IsDir()})
		if len(entries) >= maxEntries {
			return errStopWalk
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStopWalk) {
		return nil, walkErr
	}
	return entries, nil
}

func (a *Adapter) Search(ctx context.Context, workspace, query string, maxMatches int) ([]ports.WorkspaceMatch, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query is empty")
	}
	if maxMatches <= 0 {
		maxMatches = 50
	}
	var matches []ports.WorkspaceMatch
	walkErr := filepath.WalkDir(workspace, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(workspace, current)
		if err != nil {
			return err
		}
		if strings.Contains(relative, query) {
			matches = append(matches, ports.WorkspaceMatch{Path: relative})
			if len(matches) >= maxMatches {
				return errStopWalk
			}
		}
		if matched, stop := searchFileContents(current, relative, query, maxMatches, &matches); matched && stop {
			return errStopWalk
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStopWalk) {
		return nil, walkErr
	}
	return matches, nil
}

func searchFileContents(absolute, relative, query string, maxMatches int, matches *[]ports.WorkspaceMatch) (matched, stop bool) {
	info, err := os.Stat(absolute)
	if err != nil || info.Size() > maxScannedFileBytes {
		return false, false
	}
	file, err := os.Open(absolute)
	if err != nil {
		return false, false
	}
	defer file.Close()
	if isLikelyBinary(file) {
		return false, false
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxScannedFileBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if strings.Contains(line, query) {
			*matches = append(*matches, ports.WorkspaceMatch{Path: relative, Line: lineNumber, Text: truncateText(strings.TrimSpace(line), maxSearchLineLength)})
			if len(*matches) >= maxMatches {
				return true, true
			}
		}
	}
	return false, false
}

func isLikelyBinary(file *os.File) bool {
	defer file.Seek(0, io.SeekStart)
	buffer := make([]byte, 512)
	n, _ := file.Read(buffer)
	return bytes.IndexByte(buffer[:n], 0) != -1
}

func (a *Adapter) ReadFile(ctx context.Context, workspace, path string, startLine, endLine int) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	target, err := safeWorkspacePath(workspace, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("path is a directory")
	}
	const maxReadLines = 2000
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine < startLine || endLine-startLine+1 > maxReadLines {
		endLine = startLine + maxReadLines - 1
	}
	file, err := os.Open(target)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxScannedFileBytes)
	var builder strings.Builder
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if lineNumber < startLine {
			continue
		}
		if lineNumber > endLine {
			break
		}
		builder.WriteString(scanner.Text())
		builder.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func safeWorkspacePath(workspace, relative string) (string, error) {
	relative = strings.TrimPrefix(relative, "/")
	if relative == "" {
		relative = "."
	}
	cleanWorkspace := filepath.Clean(workspace)
	cleanJoined := filepath.Clean(filepath.Join(cleanWorkspace, relative))
	if cleanJoined != cleanWorkspace && !strings.HasPrefix(cleanJoined, cleanWorkspace+string(filepath.Separator)) {
		return "", errors.New("path escapes repository workspace")
	}
	return cleanJoined, nil
}

func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "...(truncated)"
}

func (a *Adapter) Commit(ctx context.Context, workspace, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("commit message is empty")
	}
	if _, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "add", "-A"}, Dir: workspace}); err != nil {
		return "", err
	}
	if _, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "-c", "user.name=Eggy", "-c", "user.email=eggy@localhost", "commit", "-m", message}, Dir: workspace}); err != nil {
		return "", err
	}
	result, err := a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "rev-parse", "HEAD"}, Dir: workspace})
	return strings.TrimSpace(result.Stdout), err
}

func (a *Adapter) Push(ctx context.Context, workspace, branch string) error {
	if !validBranch(branch) {
		return errors.New("invalid push branch")
	}
	cleanup, environment, err := a.askpass(filepath.Dir(workspace))
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = a.runner.Execute(ctx, ports.Command{Argv: []string{"git", "push", "origin", "HEAD:refs/heads/" + branch}, Dir: workspace, Env: environment})
	return err
}

func (a *Adapter) CreatePullRequest(ctx context.Context, repository ports.Repository, branch, title, body string) (ports.PullRequest, error) {
	owner, name, err := repositorySlug(repository.CloneURL)
	if err != nil {
		return ports.PullRequest{}, err
	}
	payload, _ := json.Marshal(map[string]string{"head": branch, "base": repository.BaseBranch, "title": title, "body": body})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiBase+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/pulls", bytes.NewReader(payload))
	if err != nil {
		return ports.PullRequest{}, err
	}
	request.Header.Set("Authorization", "Bearer "+a.token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	response, err := a.http.Do(request)
	if err != nil {
		return ports.PullRequest{}, fmt.Errorf("GitHub request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return ports.PullRequest{}, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	var result struct {
		URL    string `json:"html_url"`
		Number int    `json:"number"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return ports.PullRequest{}, err
	}
	return ports.PullRequest{URL: result.URL, Number: result.Number}, nil
}

func (a *Adapter) RepositorySummary(ctx context.Context, repository ports.Repository) (ports.RepositorySummary, error) {
	owner, name, err := repositorySlug(repository.CloneURL)
	if err != nil {
		return ports.RepositorySummary{}, err
	}
	var payload struct {
		Description   string `json:"description"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		HTMLURL       string `json:"html_url"`
	}
	if err := a.githubGet(ctx, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), &payload); err != nil {
		return ports.RepositorySummary{}, err
	}
	return ports.RepositorySummary{Body: payload.Description, DefaultBranch: payload.DefaultBranch, Private: payload.Private, URL: payload.HTMLURL}, nil
}

func (a *Adapter) Issue(ctx context.Context, repository ports.Repository, number int) (ports.RepositorySummary, error) {
	owner, name, err := repositorySlug(repository.CloneURL)
	if err != nil {
		return ports.RepositorySummary{}, err
	}
	return a.issueLikeSummary(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(owner), url.PathEscape(name), number))
}

func (a *Adapter) PullRequestSummary(ctx context.Context, repository ports.Repository, number int) (ports.RepositorySummary, error) {
	owner, name, err := repositorySlug(repository.CloneURL)
	if err != nil {
		return ports.RepositorySummary{}, err
	}
	return a.issueLikeSummary(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(name), number))
}

func (a *Adapter) issueLikeSummary(ctx context.Context, path string) (ports.RepositorySummary, error) {
	var payload struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	if err := a.githubGet(ctx, path, &payload); err != nil {
		return ports.RepositorySummary{}, err
	}
	return ports.RepositorySummary{Number: payload.Number, Title: payload.Title, State: payload.State, Body: payload.Body, URL: payload.HTMLURL}, nil
}

func (a *Adapter) Checks(ctx context.Context, repository ports.Repository, ref string) ([]ports.CheckRun, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, errors.New("ref is required")
	}
	owner, name, err := repositorySlug(repository.CloneURL)
	if err != nil {
		return nil, err
	}
	var payload struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs", url.PathEscape(owner), url.PathEscape(name), url.PathEscape(ref))
	if err := a.githubGet(ctx, path, &payload); err != nil {
		return nil, err
	}
	runs := make([]ports.CheckRun, 0, len(payload.CheckRuns))
	for _, run := range payload.CheckRuns {
		runs = append(runs, ports.CheckRun{Name: run.Name, Status: run.Status, Conclusion: run.Conclusion, URL: run.HTMLURL})
	}
	return runs, nil
}

func (a *Adapter) githubGet(ctx context.Context, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiBase+path, nil)
	if err != nil {
		return err
	}
	if a.token != "" {
		request.Header.Set("Authorization", "Bearer "+a.token)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := a.http.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

func (a *Adapter) askpass(directory string) (func(), map[string]string, error) {
	file, err := os.CreateTemp(directory, ".eggy-askpass-")
	if err != nil {
		return nil, nil, err
	}
	path := file.Name()
	content := "#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' x-access-token ;;\n  *) printf '%s\\n' \"$EGGY_GITHUB_TOKEN\" ;;\nesac\n"
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(path)
		return nil, nil, err
	}
	if err := file.Chmod(0o700); err != nil {
		file.Close()
		os.Remove(path)
		return nil, nil, err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return nil, nil, err
	}
	return func() { _ = os.Remove(path) }, map[string]string{"GIT_ASKPASS": path, "EGGY_GITHUB_TOKEN": a.token, "GIT_TERMINAL_PROMPT": "0"}, nil
}

func repositorySlug(cloneURL string) (string, string, error) {
	trimmed := strings.TrimSuffix(cloneURL, ".git")
	if strings.HasPrefix(trimmed, "git@") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			return splitSlug(parts[1])
		}
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", "", err
	}
	return splitSlug(strings.TrimPrefix(parsed.Path, "/"))
}

func splitSlug(value string) (string, string, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("clone URL does not identify owner/repository")
	}
	return parts[0], parts[1], nil
}

func validBranch(branch string) bool {
	if branch == "" || strings.ContainsAny(branch, " ~^:?*[\\") || strings.Contains(branch, "..") || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return false
	}
	return true
}
