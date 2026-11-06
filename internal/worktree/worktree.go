// Package worktree makes the git worktree a task runs in and removes it when
// the task is done, keeping any work that is not safe elsewhere.
package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Dir is the folder in a main checkout that holds the worktrees Agenthail makes.
const Dir = ".worktrees"

type Spec struct {
	Cwd    string
	Branch string
	Base   string
	Name   string
}

// Plan is a worktree resolved but not yet made: nothing on disk has changed.
type Plan struct {
	RepoRoot     string
	Toplevel     string
	WorktreePath string
	Cwd          string
	Branch       string
	BaseRef      string
	BaseCommit   string
	// Behind counts the commits the base's branch is behind its upstream,
	// when the base defaulted to HEAD.
	Behind int
}

var ErrNotRepository = errors.New("the folder is not in a git repository")

func NewPlan(ctx context.Context, spec Spec) (Plan, error) {
	cwd, err := filepath.EvalSymlinks(spec.Cwd)
	if err != nil {
		return Plan{}, err
	}
	toplevel, err := git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return Plan{}, ErrNotRepository
	}
	main, err := mainWorktree(ctx, cwd, toplevel)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{RepoRoot: main, Toplevel: toplevel, Branch: spec.Branch}
	if plan.Branch == "" {
		plan.Branch = defaultBranch(spec.Name)
	}
	if _, err := git(ctx, cwd, "check-ref-format", "--branch", plan.Branch); err != nil {
		return Plan{}, fmt.Errorf("%q is not a valid branch name", plan.Branch)
	}
	if exists, err := gitOK(ctx, cwd, "show-ref", "--verify", "--quiet", "refs/heads/"+plan.Branch); err != nil || exists {
		return Plan{}, errors.Join(err, fmt.Errorf("branch %s already exists", plan.Branch))
	}
	if spec.Base == "" {
		if plan.BaseCommit, err = git(ctx, cwd, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
			return Plan{}, fmt.Errorf("the checkout has no commit to start from: %w", err)
		}
		plan.BaseRef, _ = git(ctx, cwd, "symbolic-ref", "--quiet", "--short", "HEAD")
		if plan.BaseRef == "" {
			plan.BaseRef = plan.BaseCommit
		} else if behind, err := git(ctx, cwd, "rev-list", "--count", "HEAD..@{upstream}"); err == nil {
			plan.Behind, _ = strconv.Atoi(behind)
		}
	} else {
		if plan.BaseCommit, err = git(ctx, cwd, "rev-parse", "--verify", spec.Base+"^{commit}"); err != nil {
			return Plan{}, fmt.Errorf("base %s is not a commit here", spec.Base)
		}
		plan.BaseRef = spec.Base
	}
	plan.WorktreePath = filepath.Join(plan.RepoRoot, Dir, Slug(plan.Branch))
	if _, err := os.Lstat(plan.WorktreePath); err == nil {
		return Plan{}, fmt.Errorf("%s already exists", plan.WorktreePath)
	}
	if plan.Cwd, _ = plan.Translate(cwd); plan.Cwd == "" {
		plan.Cwd = plan.WorktreePath
	}
	return plan, nil
}

// Translate is the folder in the worktree at the same place a folder holds in
// the checkout it was made from; false for a folder outside that checkout.
func (p Plan) Translate(path string) (string, bool) {
	rel, err := filepath.Rel(p.Toplevel, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(p.WorktreePath, rel), true
}

// Add makes the planned worktree and branch, and has the main checkout ignore
// the worktrees folder. The session starts in the planned folder, or the
// worktree's top when that folder is not in the base commit.
func Add(ctx context.Context, plan Plan) (Plan, error) {
	if err := excludeWorktrees(ctx, plan.RepoRoot); err != nil {
		return plan, err
	}
	if _, err := git(ctx, plan.RepoRoot, "worktree", "add", "--quiet", "-b", plan.Branch, plan.WorktreePath, plan.BaseCommit); err != nil {
		return plan, err
	}
	if info, err := os.Stat(plan.Cwd); err != nil || !info.IsDir() {
		plan.Cwd = plan.WorktreePath
	}
	return plan, nil
}

// Discard takes back a worktree Add made for a session that never started.
func Discard(ctx context.Context, plan Plan) error {
	var errs []error
	if _, err := os.Stat(plan.WorktreePath); err == nil {
		if _, err := git(ctx, plan.RepoRoot, "worktree", "remove", "--force", plan.WorktreePath); err != nil {
			errs = append(errs, err)
		}
	}
	if exists, _ := gitOK(ctx, plan.RepoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+plan.Branch); exists {
		tip, _ := git(ctx, plan.RepoRoot, "rev-parse", "refs/heads/"+plan.Branch)
		if tip == plan.BaseCommit {
			if _, err := git(ctx, plan.RepoRoot, "branch", "-D", plan.Branch); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// mainWorktree is the repository's main checkout. A submodule's git
// directory lives in its superproject's, so its checkout comes from
// core.worktree rather than beside that directory.
func mainWorktree(ctx context.Context, cwd, toplevel string) (string, error) {
	paths, err := git(ctx, cwd, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", err
	}
	gitDir, common, _ := strings.Cut(paths, "\n")
	if gitDir == common {
		return toplevel, nil
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	if configured, err := git(ctx, cwd, "--git-dir="+common, "config", "core.worktree"); err == nil && configured != "" {
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(common, configured)
		}
		return filepath.EvalSymlinks(configured)
	}
	return "", fmt.Errorf("the repository has no main checkout to hold worktrees")
}

// excludeWorktrees keeps the worktrees folder out of the main checkout's
// status through its local exclude file, never the repository's .gitignore.
func excludeWorktrees(ctx context.Context, repoRoot string) error {
	if ignored, err := gitOK(ctx, repoRoot, "check-ignore", "--quiet", Dir+"/probe"); err != nil || ignored {
		return err
	}
	exclude, err := git(ctx, repoRoot, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	current, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	line := "/" + Dir + "/\n"
	if len(current) > 0 && current[len(current)-1] != '\n' {
		line = "\n" + line
	}
	file, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(line); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func defaultBranch(name string) string {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if name == "" {
		buf := make([]byte, 4)
		_, _ = rand.Read(buf)
		name = hex.EncodeToString(buf)
	}
	return "ah/" + name
}

// Slug is the folder name for a branch: its slashes become dashes.
func Slug(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}
