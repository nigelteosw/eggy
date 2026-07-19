package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

var ErrDiffTooLarge = errors.New("repository diff exceeds configured output limit")

func New(runner ports.Runner, token, apiBase string, client *http.Client) *Adapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &Adapter{runner: runner, token: token, apiBase: strings.TrimRight(apiBase, "/"), http: client}
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
