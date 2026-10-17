package marketplace

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// useAPI points the fetcher at a test server for the duration of one test.
func useAPI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
	return srv
}

func TestResolveDefaultBranch(t *testing.T) {
	useAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r":
			_, _ = io.WriteString(w, `{"default_branch":"main"}`)
		case "/repos/o/r/commits/main":
			if r.Header.Get("Accept") != "application/vnd.github.sha" {
				http.Error(w, "wrong accept", http.StatusNotAcceptable)
				return
			}
			_, _ = io.WriteString(w, testSHA)
		default:
			http.NotFound(w, r)
		}
	})
	gh := newGitHub()
	sha, err := gh.resolve(context.Background(), "o/r", "")
	if err != nil {
		t.Fatal(err)
	}
	if sha != testSHA {
		t.Fatalf("sha = %q, want %q", sha, testSHA)
	}
}

func TestResolveRef(t *testing.T) {
	useAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/commits/v1" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, testSHA+"\n")
	})
	sha, err := newGitHub().resolve(context.Background(), "o/r", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if sha != testSHA {
		t.Fatalf("sha = %q", sha)
	}
}

func TestRateLimitError(t *testing.T) {
	useAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1800000000")
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := newGitHub().resolve(context.Background(), "o/r", "main")
	var rl RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want RateLimitError", err)
	}
	if rl.Reset.Unix() != 1800000000 {
		t.Fatalf("reset = %v", rl.Reset)
	}
}

func TestNoAuthorizationHeader(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret-token")
	var seen atomic.Int32
	useAPI(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization sent: %q", got)
		}
		if r.Header.Get("User-Agent") != "eggy" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		_, _ = io.WriteString(w, testSHA)
	})
	if _, err := newGitHub().resolve(context.Background(), "o/r", "v1"); err != nil {
		t.Fatal(err)
	}
	if seen.Load() == 0 {
		t.Fatal("server saw no request")
	}
}

func TestTarballCached(t *testing.T) {
	archive := buildArchive(t, []archiveEntry{
		{name: "o-r-" + testSHA + "/skills/pdf/SKILL.md", body: []byte("---\nname: pdf\ndescription: Fill\n---\n")},
	})
	body, err := io.ReadAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	useAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/tarball/"+testSHA {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		_, _ = w.Write(body)
	})
	gh := newGitHub()
	first, err := gh.tarball(context.Background(), "o/r", testSHA)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := first.Files["skills/pdf/SKILL.md"]; !ok {
		t.Fatalf("tarball files = %v", keys(first.Files))
	}
	if _, err := gh.tarball(context.Background(), "o/r", testSHA); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("tarball fetched %d times, want 1", hits.Load())
	}
}

func TestRedirectMustStayHTTPS(t *testing.T) {
	var target *httptest.Server
	target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "should not be fetched")
	}))
	t.Cleanup(target.Close)
	useAPI(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/x.tar.gz", http.StatusFound)
	})
	_, err := newGitHub().tarball(context.Background(), "o/r", testSHA)
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v, want refusal of a non-https redirect", err)
	}
}
