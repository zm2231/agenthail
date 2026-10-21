package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const moveGitTimeout = 2 * time.Minute

// moveCheckout is the working tree a moving session runs in, in terms both
// hosts share: the repository's canonical key, the folder inside it and the
// commit checked out there.
type moveCheckout struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Commit     string `json:"commit"`
	Branch     string `json:"branch,omitempty"`
}

// sourceCheckout reads where cwd sits in its repository. Only a clean tree
// whose commit a remote already has can move: the target fetches that commit
// and checks it out.
func sourceCheckout(ctx context.Context, cwd string) (moveCheckout, error) {
	ctx, cancel := context.WithTimeout(ctx, moveGitTimeout)
	defer cancel()
	repository, notRepository, err := resolveCatalogRepository(ctx, cwd, nil)
	if notRepository {
		return moveCheckout{}, fmt.Errorf("%s is not in a git repository; only a session in a repository with a remote can move", cwd)
	}
	if err != nil {
		return moveCheckout{}, err
	}
	if repository.bare || repository.repository == nil {
		return moveCheckout{}, fmt.Errorf("the repository at %s has no remote the other host could fetch from", repository.toplevel)
	}
	canonical, err := canonicalCatalogPath(cwd)
	if err != nil {
		return moveCheckout{}, err
	}
	rel, err := filepath.Rel(repository.toplevel, canonical)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return moveCheckout{}, fmt.Errorf("%s is outside its repository %s", cwd, repository.toplevel)
	}
	status, err := catalogGit(ctx, repository.toplevel, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return moveCheckout{}, err
	}
	if strings.TrimSpace(status) != "" {
		return moveCheckout{}, fmt.Errorf("the working tree at %s has uncommitted changes; commit and push them first", repository.toplevel)
	}
	commit, err := catalogGit(ctx, repository.toplevel, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return moveCheckout{}, err
	}
	remotes, err := catalogGit(ctx, repository.toplevel, "branch", "--remotes", "--contains", commit)
	if err != nil {
		return moveCheckout{}, err
	}
	if strings.TrimSpace(remotes) == "" {
		return moveCheckout{}, fmt.Errorf("commit %s at %s is on no remote branch; push it first", commit[:min(12, len(commit))], repository.toplevel)
	}
	branch, _ := catalogGit(ctx, repository.toplevel, "symbolic-ref", "--quiet", "--short", "HEAD")
	checkout := moveCheckout{Repository: repository.repository.CanonicalKey, Path: filepath.ToSlash(rel), Commit: commit, Branch: branch}
	if checkout.Path == "." {
		checkout.Path = ""
	}
	return checkout, nil
}

// validMoveCheckout refuses a checkout a source could use to reach outside
// the target's repository.
func validMoveCheckout(checkout moveCheckout) error {
	if checkout.Repository == "" || len(checkout.Commit) < 40 || strings.Trim(checkout.Commit, "0123456789abcdef") != "" {
		return fmt.Errorf("the move names no repository and full commit")
	}
	if checkout.Path != "" && (path.IsAbs(checkout.Path) || path.Clean(checkout.Path) != checkout.Path || checkout.Path == ".." || strings.HasPrefix(checkout.Path, "../") || strings.Contains(checkout.Path, `\`)) {
		return fmt.Errorf("the move names an unsafe folder %q", checkout.Path)
	}
	if checkout.Branch != "" && (strings.HasPrefix(checkout.Branch, "-") || strings.ContainsAny(checkout.Branch, " ~^:?*[\\\x00")) {
		return fmt.Errorf("the move names an unsafe branch %q", checkout.Branch)
	}
	return nil
}

// repositoryCheckouts are this host's main checkouts of a repository, found
// through its added projects and the projects its sessions run in.
func (d *Daemon) repositoryCheckouts(ctx context.Context, canonical string) ([]string, error) {
	candidates := map[string]bool{}
	records, err := d.Registry.ListProjects()
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.ArchivedAt.IsZero() && record.WorkspaceRoot != "" {
			candidates[record.WorkspaceRoot] = true
		}
	}
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		return nil, err
	}
	for _, state := range snapshot.Sessions {
		if state.HostID != "" {
			continue
		}
		var project CatalogProject
		if json.Unmarshal(state.Project, &project) == nil && project.RootPath != "" && project.Repository != nil && project.Repository.CanonicalKey == canonical {
			candidates[project.RootPath] = true
		}
	}
	found := map[string]bool{}
	for candidate := range candidates {
		if info, err := os.Stat(candidate); err != nil || !info.IsDir() {
			continue
		}
		gitCtx, cancel := context.WithTimeout(ctx, catalogIdentityGitTimeout)
		repository, _, err := resolveCatalogRepository(gitCtx, candidate, nil)
		cancel()
		if err != nil || repository.bare || repository.repository == nil || repository.repository.CanonicalKey != canonical {
			continue
		}
		found[repository.mainRoot] = true
	}
	roots := make([]string, 0, len(found))
	for root := range found {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots, nil
}

// moveTarget is where an arriving session runs on this host.
type moveTarget struct {
	main     string
	checkout string
	cwd      string
	worktree bool
}

// resolveMoveTarget finds this host's checkout of the moving session's
// repository, fetching its commit when the checkout lacks it. The session
// runs in the main checkout when that already sits clean at the commit, and
// otherwise in a worktree made for the move.
func (d *Daemon) resolveMoveTarget(ctx context.Context, moveID string, checkout moveCheckout) (moveTarget, error) {
	if err := validMoveCheckout(checkout); err != nil {
		return moveTarget{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, moveGitTimeout)
	defer cancel()
	roots, err := d.repositoryCheckouts(ctx, checkout.Repository)
	if err != nil {
		return moveTarget{}, err
	}
	switch len(roots) {
	case 0:
		return moveTarget{}, fmt.Errorf("this host has no checkout of %s; clone it and add it with agenthail projects add", checkout.Repository)
	case 1:
	default:
		return moveTarget{}, fmt.Errorf("this host has more than one checkout of %s (%s)", checkout.Repository, strings.Join(roots, ", "))
	}
	main := roots[0]
	if _, err := catalogGit(ctx, main, "cat-file", "-e", checkout.Commit+"^{commit}"); err != nil {
		if _, fetchErr := catalogGit(ctx, main, "fetch", "--quiet", "--no-tags", "origin"); fetchErr != nil {
			return moveTarget{}, fmt.Errorf("fetch %s in %s: %w", checkout.Repository, main, fetchErr)
		}
		if _, err := catalogGit(ctx, main, "cat-file", "-e", checkout.Commit+"^{commit}"); err != nil {
			return moveTarget{}, fmt.Errorf("commit %s is not in %s even after a fetch", checkout.Commit[:12], main)
		}
	}
	target := moveTarget{main: main, checkout: main}
	head, _ := catalogGit(ctx, main, "rev-parse", "--verify", "HEAD^{commit}")
	status, statusErr := catalogGit(ctx, main, "status", "--porcelain", "--untracked-files=normal")
	if head != checkout.Commit || statusErr != nil || strings.TrimSpace(status) != "" {
		target.checkout = moveWorktreePath(main, moveID)
		target.worktree = true
	}
	target.cwd = filepath.Join(target.checkout, filepath.FromSlash(checkout.Path))
	return target, nil
}

// moveWorktreePath is the worktree a move makes in main when the main
// checkout is not clean at the moving commit.
func moveWorktreePath(main, moveID string) string {
	return filepath.Join(main, ".worktrees", "move-"+moveID[:8])
}

// ensureMoveCheckout makes the worktree a move runs in. A worktree an
// earlier attempt of the same move made is used again once it sits at the
// commit.
func ensureMoveCheckout(ctx context.Context, target moveTarget, checkout moveCheckout) error {
	ctx, cancel := context.WithTimeout(ctx, moveGitTimeout)
	defer cancel()
	if target.worktree {
		if _, err := os.Stat(target.checkout); err == nil {
			head, err := catalogGit(ctx, target.checkout, "rev-parse", "--verify", "HEAD^{commit}")
			if err != nil || head != checkout.Commit {
				return fmt.Errorf("%s exists and is not at commit %s", target.checkout, checkout.Commit[:12])
			}
		} else {
			args := []string{"worktree", "add", "--quiet"}
			if checkout.Branch != "" {
				if _, err := catalogGit(ctx, target.main, "rev-parse", "--verify", "--quiet", "refs/heads/"+checkout.Branch); err != nil {
					args = append(args, "-b", checkout.Branch)
				} else {
					args = append(args, "--detach")
				}
			} else {
				args = append(args, "--detach")
			}
			if _, err := catalogGit(ctx, target.main, append(args, target.checkout, checkout.Commit)...); err != nil {
				return fmt.Errorf("make a worktree for the session at %s: %w", target.checkout, err)
			}
		}
	}
	if info, err := os.Stat(target.cwd); err != nil || !info.IsDir() {
		return fmt.Errorf("folder %s does not exist at commit %s", target.cwd, checkout.Commit[:12])
	}
	// A symlink in the repository could otherwise run the session outside it.
	resolvedCheckout, err := filepath.EvalSymlinks(target.checkout)
	if err != nil {
		return err
	}
	resolvedCwd, err := filepath.EvalSymlinks(target.cwd)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(resolvedCheckout, resolvedCwd); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("folder %s leads outside the checkout %s", target.cwd, target.checkout)
	}
	return nil
}
