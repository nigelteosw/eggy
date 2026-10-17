package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// apiBase is GitHub's REST API. It is a package variable only so tests can point
// it at a server. It is not configurable: an operator-set host would send every
// catalog request, and any archive it returns, wherever the config names.
var apiBase = "https://api.github.com"

const (
	cacheTTL = 15 * time.Minute
	cacheMax = 4
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// RateLimitError reports that GitHub refused a request for the rate limit.
// Reset is when the unauthenticated quota is restored.
type RateLimitError struct {
	Reset time.Time
}

func (e RateLimitError) Error() string {
	if e.Reset.IsZero() {
		return "GitHub rate limit reached"
	}
	return "GitHub rate limit reached; resets at " + e.Reset.UTC().Format(time.RFC3339)
}

// github fetches catalogs and plugin trees from GitHub without a credential.
// Every request is anonymous, so no token from the environment or the config
// ever leaves Eggy on catalog traffic.
type github struct {
	client *http.Client
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]cachedTree
}

type cachedTree struct {
	tree Tree
	at   time.Time
}

func newGitHub() *github {
	return &github{
		client: &http.Client{
			Timeout: 2 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				if req.URL.Scheme != "https" {
					return fmt.Errorf("refusing non-https redirect to %s", req.URL.Redacted())
				}
				return nil
			},
		},
		now:   time.Now,
		cache: map[string]cachedTree{},
	}
}

// resolve turns a branch, tag, or empty ref into the commit it names now.
// An empty ref means the repository's default branch.
func (g *github) resolve(ctx context.Context, repo, ref string) (string, error) {
	if ref == "" {
		var info struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := g.getJSON(ctx, "/repos/"+repo, &info); err != nil {
			return "", err
		}
		if info.DefaultBranch == "" {
			return "", fmt.Errorf("github %s: no default branch", repo)
		}
		ref = info.DefaultBranch
	}
	resp, err := g.get(ctx, "/repos/"+repo+"/commits/"+url.PathEscape(ref), "application/vnd.github.sha")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(body))
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("github %s@%s: unexpected commit response", repo, ref)
	}
	return sha, nil
}

// tarball returns one repository snapshot at sha. Recent snapshots are kept for
// a short time, so a check and the install that follows it do not fetch twice.
func (g *github) tarball(ctx context.Context, repo, sha string) (Tree, error) {
	key := repo + "@" + sha
	now := g.now()
	g.mu.Lock()
	if hit, ok := g.cache[key]; ok && now.Sub(hit.at) < cacheTTL {
		g.mu.Unlock()
		return hit.tree, nil
	}
	g.mu.Unlock()

	resp, err := g.get(ctx, "/repos/"+repo+"/tarball/"+sha, "")
	if err != nil {
		return Tree{}, err
	}
	defer resp.Body.Close()
	tree, err := readArchive(resp.Body)
	if err != nil {
		return Tree{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.cache) >= cacheMax {
		g.evictOldest()
	}
	g.cache[key] = cachedTree{tree: tree, at: g.now()}
	return tree, nil
}

func (g *github) evictOldest() {
	var oldestKey string
	var oldest time.Time
	for key, entry := range g.cache {
		if oldestKey == "" || entry.at.Before(oldest) {
			oldestKey, oldest = key, entry.at
		}
	}
	delete(g.cache, oldestKey)
}

func (g *github) getJSON(ctx context.Context, path string, into any) error {
	resp, err := g.get(ctx, path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(into)
}

// get makes one anonymous request. A success returns the open response for
// the caller to read and close; a rate-limit refusal or any other failure
// returns an error.
func (g *github) get(ctx context.Context, path, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "eggy")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	if rateLimited(resp) {
		resp.Body.Close()
		return nil, RateLimitError{Reset: resetTime(resp)}
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("github %s: %s", path, resp.Status)
	}
	return resp, nil
}

func rateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0"
}

func resetTime(resp *http.Response) time.Time {
	unix, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}
