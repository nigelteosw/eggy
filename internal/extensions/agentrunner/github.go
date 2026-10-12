package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultGitHubAPI is the only host the extension talks to. Tests point the
// client at an httptest server through the baseURL field.
const defaultGitHubAPI = "https://api.github.com"

// maxGitHubBody bounds every response read, so a hostile or broken endpoint
// cannot make the process buffer without limit.
const maxGitHubBody = 1 << 20

// GitHub is the narrow set of calls the extension makes with an account's PAT.
// Its methods are the whole surface: there is deliberately no merge, approve,
// review, tag, delete-ref or force-push, and a test pins that.
type GitHub struct {
	client  *http.Client
	baseURL string
}

// NewGitHub returns a client using client, or a 30-second-timeout client when
// client is nil.
func NewGitHub(client *http.Client) *GitHub {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitHub{client: client, baseURL: defaultGitHubAPI}
}

// Identity is the account a PAT belongs to, as GitHub reports it.
type Identity struct {
	Login        string
	ID           int64
	NoReplyEmail string
}

// PullRequest is what the publisher asks GitHub to open.
type PullRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Draft bool   `json:"draft"`
}

// do sends one request with the PAT as a bearer token and returns the status
// and a bounded body. The token goes only in the Authorization header.
func (g *GitHub) do(ctx context.Context, token, method, path string, in any) (int, []byte, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("github %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGitHubBody))
	if err != nil {
		return 0, nil, fmt.Errorf("github %s %s: read: %w", method, path, err)
	}
	return resp.StatusCode, raw, nil
}

// Identity reports who the token belongs to. The no-reply address is what
// commits are authored with, so model text never chooses the author.
func (g *GitHub) Identity(ctx context.Context, token string) (Identity, error) {
	status, raw, err := g.do(ctx, token, http.MethodGet, "/user", nil)
	if err != nil {
		return Identity{}, err
	}
	if status != http.StatusOK {
		return Identity{}, fmt.Errorf("github identity: status %d", status)
	}
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := json.Unmarshal(raw, &user); err != nil || user.Login == "" {
		return Identity{}, errors.New("github identity: unreadable response")
	}
	return Identity{
		Login:        user.Login,
		ID:           user.ID,
		NoReplyEmail: fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, user.Login),
	}, nil
}

// CanPush reports whether the token may push to the repository. A repository
// the token cannot see is "no", not an error.
func (g *GitHub) CanPush(ctx context.Context, token, owner, repo string) (bool, error) {
	status, raw, err := g.do(ctx, token, http.MethodGet, "/repos/"+owner+"/"+repo, nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		var r struct {
			Permissions struct {
				Push bool `json:"push"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return false, errors.New("github repository: unreadable response")
		}
		return r.Permissions.Push, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("github repository: status %d", status)
	}
}

// BranchProtected asks whether a branch is protected. Any answer other than a
// clear yes or no is an error, so the caller fails closed.
func (g *GitHub) BranchProtected(ctx context.Context, token, owner, repo, branch string) (bool, error) {
	status, raw, err := g.do(ctx, token, http.MethodGet, "/repos/"+owner+"/"+repo+"/branches/"+url.PathEscape(branch), nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		var b struct {
			Protected bool `json:"protected"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			return false, errors.New("github branch: unreadable response")
		}
		return b.Protected, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("github branch protection: status %d", status)
	}
}

// CreatePR opens a pull request and returns its URL.
func (g *GitHub) CreatePR(ctx context.Context, token, owner, repo string, pr PullRequest) (string, error) {
	status, raw, err := g.do(ctx, token, http.MethodPost, "/repos/"+owner+"/"+repo+"/pulls", pr)
	if err != nil {
		return "", err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return "", fmt.Errorf("github create pull request: status %d", status)
	}
	var created struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.HTMLURL == "" {
		return "", errors.New("github create pull request: unreadable response")
	}
	return created.HTMLURL, nil
}

// FindPRByMarker looks for an existing pull request from head whose body
// carries marker. The publisher calls it after a crash, before opening a second
// PR, so an ambiguous create never produces a duplicate.
func (g *GitHub) FindPRByMarker(ctx context.Context, token, owner, repo, head, marker string) (string, bool, error) {
	path := "/repos/" + owner + "/" + repo + "/pulls?state=all&per_page=100&head=" + url.QueryEscape(owner+":"+head)
	status, raw, err := g.do(ctx, token, http.MethodGet, path, nil)
	if err != nil {
		return "", false, err
	}
	if status != http.StatusOK {
		return "", false, fmt.Errorf("github list pull requests: status %d", status)
	}
	var prs []struct {
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(raw, &prs); err != nil {
		return "", false, errors.New("github list pull requests: unreadable response")
	}
	for _, pr := range prs {
		if strings.Contains(pr.Body, marker) {
			return pr.HTMLURL, true, nil
		}
	}
	return "", false, nil
}

// ParseCloneURL accepts only https://github.com/<owner>/<repo>, with or without
// .git. Credentials in the URL and other hosts are refused, because the PAT is
// the only credential a clone should ever carry.
func ParseCloneURL(cloneURL string) (owner, repo string, err error) {
	u, err := url.Parse(cloneURL)
	if err != nil {
		return "", "", fmt.Errorf("clone_url: %w", err)
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil {
		return "", "", errors.New("clone_url must be https://github.com/<owner>/<repo>")
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("clone_url must name exactly one owner and repository")
	}
	return parts[0], parts[1], nil
}
