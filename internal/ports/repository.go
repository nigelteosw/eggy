package ports

import (
	"context"
	"time"
)

type Command struct {
	Argv      []string
	Dir       string
	Env       map[string]string
	Timeout   time.Duration
	MaxOutput int64
}

type CommandResult struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	OutputTruncated bool
}

type Runner interface {
	Create(context.Context, string) (string, error)
	Execute(context.Context, Command) (CommandResult, error)
	Destroy(context.Context, string) error
}

type Repository struct {
	Name              string
	CloneURL          string
	BaseBranch        string
	ProtectedBranches []string
}

type RepositoryCheckout interface {
	Clone(context.Context, Repository, string) error
}

type RepositorySummary struct {
	Number        int    `json:"number,omitempty"`
	Title         string `json:"title,omitempty"`
	State         string `json:"state,omitempty"`
	Body          string `json:"body,omitempty"`
	URL           string `json:"url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Private       bool   `json:"private,omitempty"`
}

type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
}

// RepositoryReader answers read-only questions about a repository checkout and
// its GitHub metadata without launching a coding agent, a branch, or a commit.
type RepositoryReader interface {
	ReadFile(ctx context.Context, workspace, path string, startLine, endLine int) (string, error)
	RepositorySummary(ctx context.Context, repository Repository) (RepositorySummary, error)
	Issue(ctx context.Context, repository Repository, number int) (RepositorySummary, error)
	ReviewSummary(ctx context.Context, repository Repository, number int) (RepositorySummary, error)
	Checks(ctx context.Context, repository Repository, ref string) ([]CheckRun, error)
}
