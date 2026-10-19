package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

const (
	catalogProjectGit    = "git"
	catalogProjectFolder = "folder"
)

type catalogProject struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	RootPath    string `json:"rootPath"`
	Kind        string `json:"kind"`
	Archived    bool   `json:"archived,omitempty"`
}

type catalogCheckout struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	DetachedHead string `json:"detachedHead,omitempty"`
	IsMain       bool   `json:"isMain"`
	Dirty        bool   `json:"dirty"`
}

type catalogIdentity struct {
	Project           *catalogProject `json:"project,omitempty"`
	Checkout          catalogCheckout `json:"checkout"`
	UnavailableReason string          `json:"unavailableReason,omitempty"`
}

// catalogIdentityTTL bounds how long a resolved workspace identity is reused.
// A change to the checkout's HEAD or index invalidates it sooner; working
// tree edits that touch neither show up in Dirty once the entry expires.
const catalogIdentityTTL = 2 * time.Minute

const catalogIdentityGitTimeout = 3 * time.Second

// catalogIdentityCache resolves each workspace path at most once per TTL. It
// is owned by the catalog discovery goroutine and is not safe for concurrent
// use.
type catalogIdentityCache struct {
	entries map[string]catalogIdentityEntry
	// known keeps the last resolved project per path past the TTL, so a git
	// failure that is not "not a repository" never moves a session.
	known   map[string]*catalogProject
	folders []registry.ProjectRecord
}

type catalogIdentityEntry struct {
	identity   catalogIdentity
	gitDir     string
	gitStamp   string
	resolvedAt time.Time
}

func newCatalogIdentityCache() *catalogIdentityCache {
	return &catalogIdentityCache{entries: map[string]catalogIdentityEntry{}, known: map[string]*catalogProject{}}
}

// setFolders replaces the registered folder projects. A changed set drops the
// cached identities, since a non-git path may now belong to a folder project.
func (c *catalogIdentityCache) setFolders(records []registry.ProjectRecord) {
	folders := make([]registry.ProjectRecord, 0, len(records))
	for _, record := range records {
		if record.Registered && record.Kind == catalogProjectFolder {
			folders = append(folders, record)
		}
	}
	if sameCatalogFolders(c.folders, folders) {
		return
	}
	c.folders = folders
	c.entries = map[string]catalogIdentityEntry{}
}

func sameCatalogFolders(left, right []registry.ProjectRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].RootPath != right[index].RootPath || left[index].DisplayName != right[index].DisplayName {
			return false
		}
	}
	return true
}

// identity resolves a session's project and checkout. prior reads the project
// last stored for the session; it is used when git fails and this cache has
// never resolved the path.
func (c *catalogIdentityCache) identity(ctx context.Context, session surface.Session, now time.Time, prior func() *catalogProject) catalogIdentity {
	path, err := catalogSessionPath(session.Cwd)
	if err != nil || path == "" {
		return catalogIdentity{Project: prior(), UnavailableReason: "workspace path is unavailable"}
	}
	if entry, found := c.entries[path]; found && now.Sub(entry.resolvedAt) < catalogIdentityTTL && catalogGitStamp(entry.gitDir) == entry.gitStamp {
		return entry.identity
	}
	identity, gitDir, transient := resolveCatalogIdentity(ctx, path, c.folders)
	if transient {
		if known, found := c.known[path]; found {
			identity.Project = known
		} else {
			identity.Project = prior()
		}
	} else {
		c.known[path] = identity.Project
	}
	if identity.UnavailableReason != "" {
		delete(c.entries, path)
		return identity
	}
	stamp := ""
	if gitDir != "" {
		stamp = catalogGitStamp(gitDir)
	}
	c.entries[path] = catalogIdentityEntry{identity: identity, gitDir: gitDir, gitStamp: stamp, resolvedAt: now}
	return identity
}

func (c *catalogIdentityCache) prune(now time.Time) {
	for path, entry := range c.entries {
		if now.Sub(entry.resolvedAt) >= catalogIdentityTTL {
			delete(c.entries, path)
		}
	}
}

// catalogGitStamp fingerprints the checkout state files that change on a
// branch switch, commit, or staging change.
func catalogGitStamp(gitDir string) string {
	if gitDir == "" {
		return ""
	}
	var stamp strings.Builder
	for _, name := range []string{"HEAD", "index"} {
		if info, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			fmt.Fprintf(&stamp, "%s:%d:%d;", name, info.ModTime().UnixNano(), info.Size())
		} else {
			fmt.Fprintf(&stamp, "%s:missing;", name)
		}
	}
	return stamp.String()
}

// resolveCatalogIdentity returns the workspace's identity, the checkout's git
// directory (empty outside a repository), and whether the project could not
// be resolved for a reason other than the path not being in a repository.
func resolveCatalogIdentity(ctx context.Context, path string, folders []registry.ProjectRecord) (catalogIdentity, string, bool) {
	identity := catalogIdentity{Checkout: catalogCheckout{ID: catalogPathID("checkout", path), Path: path}}
	existing := nearestExistingDirectory(path)
	if existing != path {
		repository, notRepository, err := resolveCatalogRepository(ctx, existing)
		switch {
		case err == nil:
			identity.Project = repository.project()
		case notRepository:
			identity.Project = catalogFolderProject(path, folders)
		default:
			identity.UnavailableReason = "repository identity is unavailable"
			return identity, "", true
		}
		return identity, "", false
	}
	repository, notRepository, err := resolveCatalogRepository(ctx, path)
	if err != nil {
		if notRepository {
			identity.Project = catalogFolderProject(path, folders)
			return identity, "", false
		}
		identity.UnavailableReason = "repository identity is unavailable"
		return identity, "", true
	}
	identity.Project = repository.project()
	identity.Checkout.Path = repository.toplevel
	identity.Checkout.ID = catalogPathID("checkout", repository.toplevel)
	checkoutCtx, cancel := context.WithTimeout(ctx, catalogIdentityGitTimeout)
	defer cancel()
	if reason := resolveCatalogCheckout(checkoutCtx, &identity.Checkout); reason != "" {
		identity.UnavailableReason = reason
	}
	return identity, repository.gitDir, false
}

type catalogRepository struct {
	toplevel    string
	commonDir   string
	gitDir      string
	projectRoot string
}

func (repository catalogRepository) project() *catalogProject {
	return &catalogProject{ID: catalogPathID("project", repository.commonDir), DisplayName: catalogProjectName(repository.projectRoot), RootPath: repository.projectRoot, Kind: catalogProjectGit}
}

// nearestExistingDirectory returns path, or its closest ancestor that still
// exists. A session whose worktree was removed keeps the project of the
// repository the worktree lived in.
func nearestExistingDirectory(path string) string {
	for candidate := path; ; candidate = filepath.Dir(candidate) {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		if parent := filepath.Dir(candidate); parent == candidate {
			return candidate
		}
	}
}

// resolveCatalogRepository reads the checkout root, the shared repository
// directory and the checkout's own git directory. Each is its own git call
// because a path may contain a newline. The project root is the main
// checkout: the parent of a ".git" common directory, the checkout itself
// when it owns its git directory (a submodule), and the common directory of
// a bare repository.
func resolveCatalogRepository(ctx context.Context, path string) (repository catalogRepository, notRepository bool, err error) {
	gitCtx, cancel := context.WithTimeout(ctx, catalogIdentityGitTimeout)
	defer cancel()
	toplevel, err := catalogGit(gitCtx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return catalogRepository{}, isCatalogNonGit(err), err
	}
	if repository.toplevel, err = canonicalCatalogPath(toplevel); err != nil {
		return catalogRepository{}, false, err
	}
	for _, read := range []struct {
		target *string
		args   []string
	}{
		{&repository.commonDir, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}},
		{&repository.gitDir, []string{"rev-parse", "--absolute-git-dir"}},
	} {
		value, err := catalogGit(gitCtx, repository.toplevel, read.args...)
		if err != nil {
			return catalogRepository{}, false, err
		}
		if *read.target, err = canonicalCatalogPath(value); err != nil {
			return catalogRepository{}, false, err
		}
	}
	switch {
	case filepath.Base(repository.commonDir) == ".git":
		repository.projectRoot = filepath.Dir(repository.commonDir)
	case repository.gitDir == repository.commonDir:
		repository.projectRoot = repository.toplevel
	default:
		repository.projectRoot = repository.commonDir
	}
	return repository, false, nil
}

func resolveCatalogCheckout(ctx context.Context, checkout *catalogCheckout) string {
	root := checkout.Path
	branch, err := catalogGit(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		checkout.Branch = branch
		if upstream, upstreamErr := catalogGit(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); upstreamErr == nil {
			checkout.IsMain = strings.TrimPrefix(upstream, "origin/") == branch
		} else {
			checkout.IsMain = branch == "main" || branch == "master"
		}
	} else if head, headErr := catalogGit(ctx, root, "rev-parse", "HEAD"); headErr == nil {
		checkout.DetachedHead = head
	} else {
		return "repository head is unavailable"
	}
	porcelain, err := catalogGit(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "repository status is unavailable"
	}
	checkout.Dirty = porcelain != ""
	return ""
}

// catalogFolderProject returns the registered folder project nearest to path,
// or nil when no registered folder contains it.
func catalogFolderProject(path string, folders []registry.ProjectRecord) *catalogProject {
	var nearest *registry.ProjectRecord
	for index := range folders {
		within, err := workspace.IsWithin(folders[index].RootPath, path)
		if err != nil || !within {
			continue
		}
		if nearest == nil || len(folders[index].RootPath) > len(nearest.RootPath) {
			nearest = &folders[index]
		}
	}
	if nearest == nil {
		return nil
	}
	return &catalogProject{ID: nearest.ID, DisplayName: nearest.DisplayName, RootPath: nearest.RootPath, Kind: catalogProjectFolder}
}

func catalogProjectName(root string) string {
	name := filepath.Base(root)
	if trimmed := strings.TrimSuffix(name, ".git"); trimmed != "" {
		return trimmed
	}
	return name
}

func catalogGit(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", &catalogGitError{args: args, stderr: stderr.String(), err: err}
	}
	return strings.TrimSuffix(string(output), "\n"), nil
}

// catalogSessionPath makes a discovered working directory absolute and
// canonical. Unlike typed input, its bytes are exact, so nothing is trimmed.
// A directory that no longer exists is canonical through its nearest existing
// ancestor, so a removed worktree keeps the same path form as its repository.
func catalogSessionPath(cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	existing := nearestExistingDirectory(abs)
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return abs, nil
	}
	rest, err := filepath.Rel(existing, abs)
	if err != nil {
		return abs, nil
	}
	return filepath.Join(resolved, rest), nil
}

func canonicalCatalogPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(resolved), nil
	}
	if _, err := os.Stat(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

func catalogPathID(prefix, path string) string {
	digest := sha256.Sum256([]byte(path))
	return prefix + "-" + hex.EncodeToString(digest[:12])
}

type catalogGitError struct {
	args   []string
	stderr string
	err    error
}

func (e *catalogGitError) Error() string {
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), strings.TrimSpace(e.stderr+" "+e.err.Error()))
}

func (e *catalogGitError) Unwrap() error { return e.err }

// isCatalogNonGit reports git's answer that the folder is outside any
// repository. Other failures, such as unreadable metadata or a dubious
// ownership refusal, are transient for identity and keep the last project.
func isCatalogNonGit(err error) bool {
	var gitErr *catalogGitError
	return errors.As(err, &gitErr) && strings.Contains(gitErr.stderr, "not a git repository")
}
