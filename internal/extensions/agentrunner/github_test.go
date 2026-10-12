package agentrunner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// newFakeGitHub serves the given handler and returns a client pointed at it.
func newFakeGitHub(t *testing.T, handler http.HandlerFunc) *GitHub {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	gh := NewGitHub(srv.Client())
	gh.baseURL = srv.URL
	return gh
}

func TestParseCloneURL(t *testing.T) {
	cases := []struct {
		url        string
		owner, rep string
		ok         bool
	}{
		{"https://github.com/o/r.git", "o", "r", true},
		{"https://github.com/o/r", "o", "r", true},
		{"git@github.com:o/r.git", "", "", false},
		{"https://gitlab.com/o/r.git", "", "", false},
		{"https://user:secret@github.com/o/r.git", "", "", false},
	}
	for _, tc := range cases {
		owner, repo, err := ParseCloneURL(tc.url)
		if tc.ok != (err == nil) || owner != tc.owner || repo != tc.rep {
			t.Errorf("ParseCloneURL(%q) = %q,%q,%v; want %q,%q ok=%v", tc.url, owner, repo, err, tc.owner, tc.rep, tc.ok)
		}
	}
}

func TestGitHubHasNoMergeOrApprove(t *testing.T) {
	typ := reflect.TypeOf(&GitHub{})
	banned := []string{"Merge", "Approve", "Review", "Tag", "Delete", "Force"}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		for _, word := range banned {
			if strings.Contains(name, word) {
				t.Fatalf("GitHub method %s must not exist", name)
			}
		}
	}
}

func TestCreatePRSendsDraftAndBearerToken(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/o/r/pull/7"}`))
	})
	url, err := gh.CreatePR(context.Background(), "tok", "o", "r", PullRequest{Title: "t", Body: "b", Head: "eggy/x", Base: "main", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/o/r/pull/7" {
		t.Fatalf("url = %q", url)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody["draft"] != true || gotBody["head"] != "eggy/x" || gotBody["base"] != "main" {
		t.Fatalf("body = %v", gotBody)
	}
}

func TestFindPRByMarker(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"html_url":"https://github.com/o/r/pull/9","body":"text <!-- eggy-run:abc -->"}]`))
	})
	url, found, err := gh.FindPRByMarker(context.Background(), "tok", "o", "r", "eggy/abc", "<!-- eggy-run:abc -->")
	if err != nil || !found || url != "https://github.com/o/r/pull/9" {
		t.Fatalf("found url=%q found=%v err=%v", url, found, err)
	}
	_, found, err = gh.FindPRByMarker(context.Background(), "tok", "o", "r", "eggy/other", "<!-- eggy-run:other -->")
	if err != nil || found {
		t.Fatalf("unrelated marker found=%v err=%v", found, err)
	}
}

func TestBranchProtectedFailsClosed(t *testing.T) {
	gh := newFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	protected, err := gh.BranchProtected(context.Background(), "tok", "o", "r", "main")
	if err == nil {
		t.Fatalf("500 from the protection check must be an error, got protected=%v", protected)
	}
}
