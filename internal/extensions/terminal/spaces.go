package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// repoCloneTimeout bounds making one repository's working copy.
const repoCloneTimeout = 10 * time.Minute

// repoState is what the service knows about working copies being made, keyed
// by the account directory and repository. It lives on the service, not on a
// connection, so a reconnect neither forgets a clone in flight (and starts a
// second into the same folder) nor forgets why the last one failed.
type repoState struct {
	mu        sync.Mutex
	preparing map[string]bool
	failed    map[string]string
}

func repoKey(dir, name string) string { return dir + "\x00" + name }

// begin marks the copy as being made, or reports that it already is.
func (r *repoState) begin(dir, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.preparing == nil {
		r.preparing, r.failed = map[string]bool{}, map[string]string{}
	}
	k := repoKey(dir, name)
	if r.preparing[k] {
		return false
	}
	r.preparing[k] = true
	delete(r.failed, k)
	return true
}

// end records how making the copy went.
func (r *repoState) end(dir, name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := repoKey(dir, name)
	delete(r.preparing, k)
	if err != nil {
		r.failed[k] = err.Error()
	}
}

// state reports whether the copy is being made, and the last failure.
func (r *repoState) state(dir, name string) (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := repoKey(dir, name)
	return r.preparing[k], r.failed[k]
}

func (s *Service) repos() []string {
	if s.opts.Repos == nil || s.opts.Mirror == nil {
		return nil
	}
	var out []string
	for _, name := range s.opts.Repos() {
		if spaceName.MatchString(name) {
			out = append(out, name)
		}
	}
	return out
}

func (s *Service) isRepo(name string) bool { return slices.Contains(s.repos(), name) }

// ensureRepos starts making, in the background, the working copy of every
// configured repository the account does not have, is not already making,
// and has not just failed to make (Update retries that). It runs after a
// core connects and on every listing, so a repository added later appears
// without a reconnect.
func (s *Service) ensureRepos(ctx context.Context, c *core) {
	for _, name := range s.repos() {
		if hasCopy(c.dir, name) {
			continue
		}
		if _, failed := s.copies.state(c.dir, name); failed != "" {
			continue
		}
		go func() { _ = s.cloneRepo(ctx, c, name) }()
	}
}

// cloneRepo makes ws/<name> from the repository's mirror. The clone runs in
// the sandbox as the account, through its core, so root never runs git in a
// tree the account controls; the mirror is the origin, so git pull works and
// git push cannot.
func (s *Service) cloneRepo(ctx context.Context, c *core, name string) (err error) {
	if !s.copies.begin(c.dir, name) {
		return nil
	}
	defer func() {
		s.copies.end(c.dir, name, err)
		if err != nil {
			s.opts.Logger.Warn("terminal: repository space not made", "repository", name, "error", err)
		}
	}()
	// Not the request's deadline: the clone outlives the request that
	// started it. The context still names the account, whose token the
	// mirror fetch uses.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), repoCloneTimeout)
	defer cancel()
	mirror, err := s.syncMirror(ctx, c, name)
	if err != nil {
		return err
	}
	return c.run(ctx, "git", "clone", "--quiet", "--no-local", mirror, filepath.Join(c.dir, "ws", name))
}

// syncMirror brings the repository's mirror up to date and names it safe in
// the account's own git config, once. Git refuses another user's repository
// otherwise, and only a user's global config can say it is safe (not -c, on
// the git in the image). The account's runs and terminals share that config,
// so its own git pull and its runs' fetches work too.
func (s *Service) syncMirror(ctx context.Context, c *core, name string) (string, error) {
	mirror, err := s.opts.Mirror(ctx, name)
	if err != nil {
		return "", err
	}
	err = c.run(ctx, "sh", "-c", `git config --global --get-all safe.directory | grep -qxF "$1" || git config --global --add safe.directory "$1"`, "sh", mirror)
	return mirror, err
}

// UpdateRepo brings a repository space up to date: the mirror from GitHub,
// then the working copy's origin from the mirror. It fetches and never
// merges: the working copy is the owner's. A missing working copy is made,
// which is also how a failed one is retried.
func (s *Service) UpdateRepo(ctx context.Context, name string) error {
	if !s.isRepo(name) {
		return ErrNotFound
	}
	c, err := s.coreFor(ctx)
	if err != nil {
		return err
	}
	if !hasCopy(c.dir, name) {
		return s.cloneRepo(ctx, c, name)
	}
	if _, err := s.syncMirror(ctx, c, name); err != nil {
		return err
	}
	return c.run(ctx, "git", "-C", filepath.Join(c.dir, "ws", name), "fetch", "--quiet", "origin")
}

// DeleteSpace ends the space's terminals and deletes its folder. A
// configured repository's space is refused: it would only be made again.
func (s *Service) DeleteSpace(ctx context.Context, name string) error {
	if !spaceName.MatchString(name) {
		return ErrNotFound
	}
	if s.isRepo(name) {
		return fmt.Errorf("%s is a configured repository; remove it from the agent runner's repositories first", name)
	}
	c, err := s.coreFor(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(c.dir, "ws", name)
	if _, err := os.Stat(dir); err != nil {
		return ErrNotFound
	}
	for _, t := range c.list() {
		if t.Space == name {
			if err := s.Kill(ctx, t.Address); err != nil {
				return err
			}
		}
	}
	return c.run(ctx, "rm", "-rf", "--", dir)
}

// Spaces lists the account's spaces, each with its terminals. A terminal with
// no space, such as one a program started itself, is listed under "".
func (s *Service) Spaces(ctx context.Context) ([]Space, error) {
	c, err := s.coreFor(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(c.dir, "ws"))
	if err != nil {
		return nil, err
	}
	s.ensureRepos(context.WithoutCancel(ctx), c)
	set := spaceSet{}
	for _, e := range entries {
		if e.IsDir() && spaceName.MatchString(e.Name()) {
			set.get(e.Name())
		}
	}
	for _, t := range c.list() {
		sp := set.get(t.Space)
		sp.Terminals = append(sp.Terminals, t)
	}
	for _, name := range s.repos() {
		sp := set.get(name)
		sp.Repo = true
		if !hasCopy(c.dir, name) {
			sp.Preparing, sp.Error = s.copies.state(c.dir, name)
		}
	}
	return set.sorted(), nil
}

// spaceSet gathers a listing's spaces by name: folders, the spaces terminals
// name, and configured repositories each add to the one entry.
type spaceSet map[string]*Space

func (set spaceSet) get(name string) *Space {
	if sp := set[name]; sp != nil {
		return sp
	}
	sp := &Space{Name: name, Terminals: []Terminal{}}
	set[name] = sp
	return sp
}

func (set spaceSet) sorted() []Space {
	out := make([]Space, 0, len(set))
	for _, sp := range set {
		out = append(out, *sp)
	}
	slices.SortFunc(out, func(a, b Space) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// CreateSpace makes an empty folder in the account's workspace. The sandbox
// user owns it, because its programs work in it.
func (s *Service) CreateSpace(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if err := s.checkNewSpace(name); err != nil {
		return err
	}
	c, err := s.coreFor(ctx)
	if err != nil {
		return err
	}
	// The folder is made by a program in the sandbox, as its owner, so the
	// extension never needs coder's ownership rights itself.
	return c.run(ctx, "mkdir", "-p", "--", filepath.Join(c.dir, "ws", name))
}

// checkNewSpace refuses a name a space cannot take.
func (s *Service) checkNewSpace(name string) error {
	if !spaceName.MatchString(name) {
		return errors.New("a space name is lowercase letters, digits, '.', '_' or '-', up to 48 characters")
	}
	if s.isRepo(name) {
		return fmt.Errorf("%s is a repository space already", name)
	}
	return nil
}

// hasCopy reports whether the repository's working copy is there: a git
// repository, not merely a folder of that name. A space the owner made with
// the same name before it was a repository is an empty folder, and git
// clones into an empty folder; one with files in it is left alone and the
// clone's refusal says why.
func hasCopy(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, "ws", name, ".git"))
	return err == nil
}
