package github

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

var _ ports.RepositoryCheckout = (*Adapter)(nil)
var _ ports.RepositoryReader = (*Adapter)(nil)

const maxScannedFileBytes = 1 << 20

func New(runner ports.Runner, token, apiBase string, client *http.Client) *Adapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &Adapter{runner: runner, token: token, apiBase: strings.TrimRight(apiBase, "/"), http: client}
}

// ValidateCloneAccess verifies that the configured remote and base branch can
// be read without downloading a checkout.
func (a *Adapter) ValidateCloneAccess(ctx context.Context, repository ports.Repository) error {
	if a.runner == nil {
		return errors.New("repository runner is unavailable")
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return fmt.Errorf("create validation workspace ID: %w", err)
	}
	workspace, err := a.runner.Create(ctx, fmt.Sprintf("validate-%x", random))
	if err != nil {
		return err
	}
	defer func() { _ = a.runner.Destroy(context.Background(), workspace) }()
	cleanup, environment, err := a.askpass(workspace)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = a.runner.Execute(ctx, ports.Command{
		Argv: []string{"git", "ls-remote", "--exit-code", "--heads", "--", repository.CloneURL, "refs/heads/" + repository.BaseBranch},
		Dir:  workspace,
		Env:  environment,
	})
	return err
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

func (a *Adapter) RepositorySummary(ctx context.Context, repository ports.Repository) (ports.RepositorySummary, error) {
	_, base, err := a.repoBase(repository)
	if err != nil {
		return ports.RepositorySummary{}, err
	}
	var payload struct {
		Description   string `json:"description"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		HTMLURL       string `json:"html_url"`
	}
	if err := a.githubGet(ctx, base, &payload); err != nil {
		return ports.RepositorySummary{}, err
	}
	return ports.RepositorySummary{Body: payload.Description, DefaultBranch: payload.DefaultBranch, Private: payload.Private, URL: payload.HTMLURL}, nil
}

func (a *Adapter) Issue(ctx context.Context, repository ports.Repository, number int) (ports.RepositorySummary, error) {
	_, base, err := a.repoBase(repository)
	if err != nil {
		return ports.RepositorySummary{}, err
	}
	return a.issueLikeSummary(ctx, fmt.Sprintf("%s/issues/%d", base, number))
}

func (a *Adapter) ReviewSummary(ctx context.Context, repository ports.Repository, number int) (ports.RepositorySummary, error) {
	_, base, err := a.repoBase(repository)
	if err != nil {
		return ports.RepositorySummary{}, err
	}
	return a.issueLikeSummary(ctx, fmt.Sprintf("%s/pulls/%d", base, number))
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
	_, base, err := a.repoBase(repository)
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
	path := fmt.Sprintf("%s/commits/%s/check-runs", base, url.PathEscape(ref))
	if err := a.githubGet(ctx, path, &payload); err != nil {
		return nil, err
	}
	runs := make([]ports.CheckRun, 0, len(payload.CheckRuns))
	for _, run := range payload.CheckRuns {
		runs = append(runs, ports.CheckRun{Name: run.Name, Status: run.Status, Conclusion: run.Conclusion, URL: run.HTMLURL})
	}
	return runs, nil
}

// repoBase resolves repository's clone URL to its owner and the escaped
// "/repos/{owner}/{name}" API path shared by every GitHub REST call above.
func (a *Adapter) repoBase(repository ports.Repository) (owner, path string, err error) {
	owner, name, err := repositorySlug(repository.CloneURL)
	if err != nil {
		return "", "", err
	}
	return owner, "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name), nil
}

func (a *Adapter) githubGet(ctx context.Context, path string, out any) error {
	return a.githubRequest(ctx, http.MethodGet, path, nil, out, http.StatusOK)
}

// githubRequest issues one GitHub REST call and decodes its response.
func (a *Adapter) githubRequest(ctx context.Context, method, path string, payload, out any, expectedStatus int) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, a.apiBase+path, body)
	if err != nil {
		return err
	}
	if a.token != "" {
		request.Header.Set("Authorization", "Bearer "+a.token)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.http.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	if out == nil {
		return nil
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
