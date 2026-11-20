package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// repoCloneTimeout bounds making one repository's working copy.
const repoCloneTimeout = 10 * time.Minute

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

// ensureRepos makes the working copy of every configured repository the
// account does not have yet. It runs after a core connects.
func (s *Service) ensureRepos(ctx context.Context, c *core) {
	for _, name := range s.repos() {
		if _, err := os.Stat(filepath.Join(c.dir, "ws", name)); err == nil {
			continue
		}
		if err := s.cloneRepo(ctx, c, name); err != nil {
			s.opts.Logger.Warn("terminal: repository space not made", "repository", name, "error", err)
		}
	}
}

// cloneRepo makes ws/<name> from the repository's mirror. The clone runs in
// the sandbox as the account, through its core, so root never runs git in a
// tree the account controls; the mirror is the origin, so git pull works and
// git push cannot.
func (s *Service) cloneRepo(ctx context.Context, c *core, name string) error {
	c.mu.Lock()
	if c.preparing[name] {
		c.mu.Unlock()
		return nil
	}
	c.preparing[name] = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.preparing, name)
		c.mu.Unlock()
	}()
	// Not the request's deadline: the clone outlives the request that
	// started it. The context still names the account, whose token the
	// mirror fetch uses.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), repoCloneTimeout)
	defer cancel()
	mirror, err := s.opts.Mirror(ctx, name)
	if err != nil {
		return err
	}
	if err := trustMirror(ctx, c, mirror); err != nil {
		return err
	}
	return c.run(ctx, "git", "clone", "--quiet", "--no-local", mirror, filepath.Join(c.dir, "ws", name))
}

// UpdateRepo brings a repository space up to date: the mirror from GitHub,
// then the working copy's origin from the mirror. It fetches and never
// merges: the working copy is the owner's. A missing working copy is made.
func (s *Service) UpdateRepo(ctx context.Context, name string) error {
	if !s.isRepo(name) {
		return ErrNotFound
	}
	c, err := s.coreFor(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(c.dir, "ws", name)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return s.cloneRepo(ctx, c, name)
	}
	mirror, err := s.opts.Mirror(ctx, name)
	if err != nil {
		return err
	}
	if err := trustMirror(ctx, c, mirror); err != nil {
		return err
	}
	return c.run(ctx, "git", "-C", dir, "fetch", "--quiet", "origin")
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

// spaceFlags marks repository spaces in a listing and adds those still being
// made, so the list shows every configured repository.
func (s *Service) spaceFlags(c *core, spaces []Space) []Space {
	repos := s.repos()
	c.mu.Lock()
	preparing := map[string]bool{}
	for name := range c.preparing {
		preparing[name] = true
	}
	c.mu.Unlock()
	seen := map[string]bool{}
	for i := range spaces {
		spaces[i].Repo = slices.Contains(repos, spaces[i].Name)
		seen[spaces[i].Name] = true
	}
	for _, name := range repos {
		if !seen[name] {
			spaces = append(spaces, Space{Name: name, Repo: true, Preparing: preparing[name], Terminals: []Terminal{}})
		}
	}
	slices.SortFunc(spaces, func(a, b Space) int { return strings.Compare(a.Name, b.Name) })
	return spaces
}

// trustMirror names the root-owned mirror safe in the account's own git
// config, once. Git refuses another user's repository otherwise, and only a
// user's global config can say it is safe (not -c, on the git in the image).
// The account's runs and terminals share that config, so its own git pull
// and its runs' fetches work too.
func trustMirror(ctx context.Context, c *core, mirror string) error {
	return c.run(ctx, "sh", "-c", `git config --global --get-all safe.directory | grep -qxF "$1" || git config --global --add safe.directory "$1"`, "sh", mirror)
}
