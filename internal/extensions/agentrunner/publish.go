package agentrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nigelteosw/eggy/internal/config"
	"github.com/nigelteosw/eggy/internal/ports"
)

// minSecretLength keeps short values, which occur in ordinary text, from being
// read as secrets. Real credentials are far longer.
const minSecretLength = 8

// change is one path the publisher will commit: a file to write, or a path to
// remove.
type change struct {
	path    string
	remove  bool
	mode    fs.FileMode
	content []byte
}

// diffWorkspace compares the agent's working tree with the base checkout and
// returns what differs. It reads files only. Git never runs in the workspace,
// because the agent controls ws/.git, and git would read that config and any
// hooks it names as root. Commits the agent made are therefore ignored, and
// only the content it left behind is published.
func diffWorkspace(ws, base string, secrets []string) ([]change, error) {
	var changes []change
	seen := map[string]bool{}
	err := filepath.WalkDir(ws, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(ws, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s is not allowed in a change", rel)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("special file %s is not allowed in a change", rel)
		}
		if err := checkChangePath(rel); err != nil {
			return err
		}
		seen[rel] = true
		info, err := entry.Info()
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := gitMode(info.Mode())
		if unchanged(filepath.Join(base, rel), content, mode) {
			return nil
		}
		for _, secret := range secrets {
			if len(secret) >= minSecretLength && bytes.Contains(content, []byte(secret)) {
				// The message names the file, never the value.
				return fmt.Errorf("%s contains a secret value; the change was not published", rel)
			}
		}
		changes = append(changes, change{path: rel, mode: mode, content: content})
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil || rel == "." {
			return err
		}
		if rel == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() || seen[rel] {
			return nil
		}
		changes = append(changes, change{path: rel, remove: true})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(changes, func(a, b change) int { return strings.Compare(a.path, b.path) })
	return changes, nil
}

// checkChangePath refuses paths a change may never touch: anything inside a
// git directory, and the workflow and CI tree, which would run new code on
// the branch's CI. The branch's CI is accepted; changing what CI runs is not.
func checkChangePath(rel string) error {
	for _, part := range strings.Split(rel, "/") {
		if part == ".git" {
			return fmt.Errorf("%s is inside a git directory", rel)
		}
	}
	if rel == ".github" || strings.HasPrefix(rel, ".github/") {
		return fmt.Errorf("%s: workflow and CI files are not changed by Eggy", rel)
	}
	return nil
}

// gitMode is the only permission git records for a file: executable or not.
// Anything else in the agent's file modes is not carried into the repository.
func gitMode(mode fs.FileMode) fs.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// unchanged reports whether the base has the same bytes and the same
// executable bit, which is all git compares.
func unchanged(basePath string, content []byte, mode fs.FileMode) bool {
	info, err := os.Lstat(basePath)
	if err != nil || !info.Mode().IsRegular() || gitMode(info.Mode()) != mode {
		return false
	}
	base, err := os.ReadFile(basePath)
	return err == nil && bytes.Equal(base, content)
}

// safeJoin joins rel under root, refusing to pass through a symlink, so a
// write can never land outside the publish checkout.
func safeJoin(root, rel string) (string, error) {
	cur := root
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%s passes through a symlink in the repository", rel)
		}
	}
	return cur, nil
}

// applyChanges writes the changes into the publish checkout.
func applyChanges(pub string, changes []change) error {
	for _, c := range changes {
		target, err := safeJoin(pub, c.path)
		if err != nil {
			return err
		}
		if c.remove {
			if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, c.content, 0o600); err != nil {
			return err
		}
		if err := os.Chmod(target, c.mode); err != nil {
			return err
		}
	}
	return nil
}

// checksPassed is true only when every configured check ran and passed, and
// nothing failed. Anything else makes the pull request a draft.
func checksPassed(configured [][]string, results []CheckResult) bool {
	if len(configured) == 0 {
		return false
	}
	for _, r := range results {
		if r.ExitCode != 0 || r.TimedOut {
			return false
		}
	}
	for _, argv := range configured {
		found := false
		for _, r := range results {
			if slices.Equal(r.Argv, argv) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// prTitle is the first line of the brief, shortened for a PR title.
func prTitle(brief string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(brief), "\n")
	if len(line) > 72 {
		line = line[:72]
	}
	return line
}

// runMarker is the stable text a PR carries, so a crash after the PR request
// can find it instead of opening a second one.
func runMarker(id string) string { return "<!-- eggy-run:" + id + " -->" }

// prBody is the description a PR carries: the summary, what changed, the checks
// the runner actually executed, and what is limited.
func prBody(run ports.AgentRun, summary string, paths []string, configured [][]string, results []CheckResult, draft bool) string {
	var b strings.Builder
	b.WriteString("## Summary\n\n" + summary + "\n\n## Changes\n\n")
	for _, p := range paths {
		b.WriteString("- `" + p + "`\n")
	}
	b.WriteString("\n## Checks run\n\n")
	if len(results) == 0 {
		b.WriteString("No checks ran.\n")
	}
	for _, r := range results {
		status := fmt.Sprintf("exit %d", r.ExitCode)
		if r.TimedOut {
			status = "timed out"
		}
		b.WriteString("- `" + strings.Join(r.Argv, " ") + "` → " + status + "\n")
	}
	b.WriteString("\n## Limitations\n\n")
	if draft {
		b.WriteString("Draft: the configured checks did not all run and pass.\n")
	} else {
		b.WriteString("Every configured check ran and passed. Eggy has not reviewed this change; a person must.\n")
	}
	b.WriteString("\n" + runMarker(run.ID) + "\n")
	return b.String()
}

// publish turns a finished coding turn into a commit, a pushed branch and a
// pull request. Each external effect is preceded by a stage record, so
// reconcile can tell after a crash which ones happened.
func (s *Service) publish(ctx context.Context, run ports.AgentRun, repo config.RepositoryConfig, results []CheckResult, summary string) error {
	run.State = ports.AgentRunPublishing
	run, err := s.opts.Store.UpdateAgentRun(ctx, run)
	if err != nil {
		return err
	}
	token, generation, err := s.opts.Credentials.token(ctx)
	if err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, "GitHub token unavailable: "+err.Error(), "")
	}
	if generation != run.PATGeneration {
		return s.finish(ctx, run, ports.AgentRunFailed, "GitHub token changed since the run started", "")
	}
	if !s.opts.Now().Before(run.MandateExpires) {
		return s.finish(ctx, run, ports.AgentRunFailed, "the run's mandate expired before publication", "")
	}
	owner, name, err := ParseCloneURL(repo.CloneURL)
	if err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, err.Error(), "")
	}
	runDir := s.opts.Workspaces.Dir(run.ID)
	pub := filepath.Join(runDir, "publish")
	home := filepath.Join(runDir, "publish-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	env, cleanup, err := gitEnv(s.opts.Workspaces.root, home, token)
	if err != nil {
		return err
	}
	defer cleanup()

	if _, err := os.Stat(pub); errors.Is(err, fs.ErrNotExist) {
		if _, err := gitOutput(ctx, env, "", token, "clone", "--no-checkout", s.opts.GitURL(repo), pub); err != nil {
			return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("publish clone: "+err.Error()), "")
		}
		if _, err := gitOutput(ctx, env, pub, token, "checkout", "--detach", run.BaseSHA); err != nil {
			return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("publish checkout: "+err.Error()), "")
		}
	}
	changes, err := diffWorkspace(filepath.Join(runDir, "ws"), pub, s.secrets(token))
	if err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact(err.Error()), "")
	}
	if len(changes) == 0 {
		return s.finish(ctx, run, ports.AgentRunCompleted, "", summary)
	}
	if err := applyChanges(pub, changes); err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("apply change: "+err.Error()), "")
	}
	if _, err := gitOutput(ctx, env, pub, token, "add", "-A", "-f"); err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("stage change: "+err.Error()), "")
	}
	identity, err := s.opts.GitHub.Identity(ctx, token)
	if err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("identity: "+err.Error()), "")
	}
	commitEnv := append(slices.Clone(env),
		"GIT_AUTHOR_NAME="+identity.Login, "GIT_AUTHOR_EMAIL="+identity.NoReplyEmail,
		"GIT_COMMITTER_NAME="+identity.Login, "GIT_COMMITTER_EMAIL="+identity.NoReplyEmail)
	message := prTitle(run.Brief) + "\n\nEggy-Run: " + run.ID + "\n"
	if _, err := gitOutput(ctx, commitEnv, pub, token, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-m", message); err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("commit: "+err.Error()), "")
	}
	head, err := gitOutput(ctx, env, pub, token, "rev-parse", "HEAD")
	if err != nil {
		return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("rev-parse: "+err.Error()), "")
	}
	paths := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.path)
	}
	draft := !checksPassed(repo.Checks, results)
	run.HeadSHA = head
	run.PRTitle = prTitle(run.Brief)
	// The body is redacted like every other stored text: the summary is the
	// model's, and nothing it wrote may reach GitHub with a live secret in it.
	run.PRBody = s.opts.Redact(prBody(run, summary, paths, repo.Checks, results, draft))
	run.Draft = draft
	run.Output = s.opts.Redact(summary)
	run.Stage = ports.PublishCommitted
	s.note(ctx, run.ID, "stage", string(ports.PublishCommitted))
	if run, err = s.opts.Store.UpdateAgentRun(ctx, run); err != nil {
		return err
	}
	return s.continuePublish(ctx, run, repo, owner, name, token)
}

// continuePublish carries a committed run through push, pull request and
// delivery, from whatever stage it last reached. Each step is idempotent given
// the stage record, so resuming never repeats a confirmed effect.
func (s *Service) continuePublish(ctx context.Context, run ports.AgentRun, repo config.RepositoryConfig, owner, name, token string) error {
	runDir := s.opts.Workspaces.Dir(run.ID)
	pub := filepath.Join(runDir, "publish")
	env, cleanup, err := gitEnv(s.opts.Workspaces.root, filepath.Join(runDir, "publish-home"), token)
	if err != nil {
		return err
	}
	defer cleanup()

	if run.Stage == ports.PublishCommitted || run.Stage == ports.PublishPushIntended {
		pushed := false
		if run.Stage == ports.PublishPushIntended {
			sha, exists, err := s.opts.GitHub.BranchSHA(ctx, token, owner, name, run.Branch)
			if err != nil {
				return err
			}
			switch {
			case exists && sha == run.HeadSHA:
				run.Stage = ports.PublishPushed
				s.note(ctx, run.ID, "stage", string(ports.PublishPushed))
				pushed = true
			case exists:
				return s.finish(ctx, run, ports.AgentRunFailed, "branch "+run.Branch+" has unexpected content; it was not overwritten", "")
			}
		}
		if !pushed {
			if err := s.preflight(ctx, run, repo, owner, name, token); err != nil {
				return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact(err.Error()), "")
			}
			run.Stage = ports.PublishPushIntended
			s.note(ctx, run.ID, "stage", string(ports.PublishPushIntended))
			if run, err = s.opts.Store.UpdateAgentRun(ctx, run); err != nil {
				return err
			}
			if _, err := gitOutput(ctx, env, pub, token, "-c", "core.hooksPath=/dev/null", "push", "origin", "HEAD:refs/heads/"+run.Branch); err != nil {
				return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("push: "+err.Error()), "")
			}
			run.Stage = ports.PublishPushed
			s.note(ctx, run.ID, "stage", string(ports.PublishPushed))
		}
		if run, err = s.opts.Store.UpdateAgentRun(ctx, run); err != nil {
			return err
		}
	}
	if run.Stage == ports.PublishPushed || run.Stage == ports.PublishPRIntended {
		if run, err = s.openPR(ctx, run, owner, name, token); err != nil {
			return s.finish(ctx, run, ports.AgentRunFailed, s.opts.Redact("pull request: "+err.Error()), "")
		}
	}
	if run.Stage == ports.PublishPROpened {
		return s.deliverPR(ctx, run)
	}
	return nil
}

// preflight refuses a push the mandate does not cover. A protected or
// unknown-protection branch fails closed.
func (s *Service) preflight(ctx context.Context, run ports.AgentRun, repo config.RepositoryConfig, owner, name, token string) error {
	if !s.opts.Now().Before(run.MandateExpires) {
		return errors.New("the run's mandate expired before the push")
	}
	if run.Branch != "eggy/"+run.ID {
		return errors.New("the run's branch is not its own")
	}
	if run.Branch == repo.BaseBranch || slices.Contains(repo.ProtectedBranches, run.Branch) {
		return errors.New("the run's branch is protected")
	}
	protected, err := s.opts.GitHub.BranchProtected(ctx, token, owner, name, run.Branch)
	if err != nil {
		return fmt.Errorf("could not confirm the branch is unprotected: %w", err)
	}
	if protected {
		return errors.New("the run's branch is protected on GitHub")
	}
	return nil
}

// openPR records the pull request for a run. It looks for an existing PR with
// the run's marker first, so a PR whose response was lost is adopted, not
// duplicated.
func (s *Service) openPR(ctx context.Context, run ports.AgentRun, owner, name, token string) (ports.AgentRun, error) {
	url, found, err := s.opts.GitHub.FindPRByMarker(ctx, token, owner, name, run.Branch, runMarker(run.ID))
	if err != nil {
		return run, err
	}
	if !found {
		run.Stage = ports.PublishPRIntended
		s.note(ctx, run.ID, "stage", string(ports.PublishPRIntended))
		if run, err = s.opts.Store.UpdateAgentRun(ctx, run); err != nil {
			return run, err
		}
		url, err = s.opts.GitHub.CreatePR(ctx, token, owner, name, PullRequest{
			Title: run.PRTitle, Body: run.PRBody, Head: run.Branch, Base: run.BaseBranch, Draft: run.Draft,
		})
		if err != nil {
			return run, err
		}
	}
	run.PRURL = url
	run.Stage = ports.PublishPROpened
	s.note(ctx, run.ID, "stage", string(ports.PublishPROpened))
	return s.opts.Store.UpdateAgentRun(ctx, run)
}

// deliverPR announces the pull request to the channel the run started from.
// The run is completed only after the announcement succeeds, so a failed
// announcement is retried by reconcile without republishing anything.
func (s *Service) deliverPR(ctx context.Context, run ports.AgentRun) error {
	text := "Pull request opened on " + run.Repository + ": " + run.PRURL
	if run.Draft {
		text = "Draft pull request opened on " + run.Repository + " (checks did not all pass): " + run.PRURL
	}
	if s.opts.Notify != nil {
		if err := s.opts.Notify(ctx, run, text); err != nil {
			return err
		}
	}
	run.Delivered = true
	run.Stage = ports.PublishDelivered
	s.note(ctx, run.ID, "stage", string(ports.PublishDelivered))
	run.State = ports.AgentRunCompleted
	_, err := s.opts.Store.UpdateAgentRun(ctx, run)
	return err
}

// reconcile resumes runs a restart interrupted during publication.
func (s *Service) reconcile(ctx context.Context) error {
	runs, err := s.opts.Store.AgentRunsInStates(ctx, ports.AgentRunPublishing)
	if err != nil {
		return err
	}
	for _, run := range runs {
		owned := ownerContext(ctx, run)
		repo, ok := s.repository(run.Repository)
		if !ok {
			if err := s.finish(owned, run, ports.AgentRunFailed, "repository no longer allowed", ""); err != nil {
				return err
			}
			continue
		}
		token, generation, err := s.opts.Credentials.token(owned)
		if err != nil || generation != run.PATGeneration {
			if err := s.finish(owned, run, ports.AgentRunFailed, "GitHub token changed or removed since the run started", ""); err != nil {
				return err
			}
			continue
		}
		owner, name, err := ParseCloneURL(repo.CloneURL)
		if err != nil {
			return s.finish(owned, run, ports.AgentRunFailed, err.Error(), "")
		}
		if err := s.continuePublish(owned, run, repo, owner, name, token); err != nil {
			return err
		}
	}
	return nil
}

// secrets is every live value a change must not contain: the configured
// secrets and this account's token.
func (s *Service) secrets(token string) []string {
	return append(s.opts.SecretValues(), token)
}
