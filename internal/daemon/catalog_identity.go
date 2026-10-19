package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/project"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type catalogCheckout struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	DetachedHead string `json:"detachedHead,omitempty"`
	IsMain       bool   `json:"isMain"`
	Dirty        bool   `json:"dirty"`
}

type catalogIdentity struct {
	Project           *project.Project `json:"project,omitempty"`
	Checkout          catalogCheckout  `json:"checkout"`
	UnavailableReason string           `json:"unavailableReason,omitempty"`
}

// catalogIdentityTTL bounds how long a resolved workspace identity is reused.
// A change to the checkout's HEAD or index invalidates it sooner; working
// tree edits that touch neither show up in Dirty once the entry expires.
const catalogIdentityTTL = 2 * time.Minute

// catalogIdentityCache resolves each workspace path at most once per TTL. It
// is owned by the catalog discovery goroutine and is not safe for concurrent
// use.
type catalogIdentityCache struct {
	entries map[string]catalogIdentityEntry
	// known keeps the last resolved project per path past the TTL, so a git
	// failure that is not "not a repository" never moves a session.
	known   map[string]*project.Project
	folders []registry.ProjectRecord
}

type catalogIdentityEntry struct {
	identity   catalogIdentity
	gitDir     string
	gitStamp   string
	resolvedAt time.Time
}

func newCatalogIdentityCache() *catalogIdentityCache {
	return &catalogIdentityCache{entries: map[string]catalogIdentityEntry{}, known: map[string]*project.Project{}}
}

// setFolders replaces the registered folder projects. A changed set drops the
// cached identities, since a non-git path may now belong to a folder project.
func (c *catalogIdentityCache) setFolders(records []registry.ProjectRecord) {
	folders := project.Folders(records)
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
func (c *catalogIdentityCache) identity(ctx context.Context, session surface.Session, now time.Time, prior func() *project.Project) catalogIdentity {
	path, err := project.SessionPath(session.Cwd)
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
// directory (empty outside a repository or for a removed checkout), and
// whether the project could not be resolved for a reason other than the path
// not being in a repository.
func resolveCatalogIdentity(ctx context.Context, path string, folders []registry.ProjectRecord) (catalogIdentity, string, bool) {
	identity := catalogIdentity{Checkout: catalogCheckout{ID: project.PathID("checkout", path), Path: path}}
	existing := project.NearestExistingDirectory(path)
	repository, notRepository, err := project.ReadRepository(ctx, existing)
	switch {
	case err == nil:
		identity.Project = repository.Project()
	case notRepository:
		identity.Project = project.NearestFolder(path, folders)
		return identity, "", false
	default:
		identity.UnavailableReason = "repository identity is unavailable"
		return identity, "", true
	}
	if existing != path {
		return identity, "", false
	}
	identity.Checkout.Path = repository.Toplevel
	identity.Checkout.ID = project.PathID("checkout", repository.Toplevel)
	checkoutCtx, cancel := context.WithTimeout(ctx, project.GitTimeout)
	defer cancel()
	if reason := resolveCatalogCheckout(checkoutCtx, &identity.Checkout); reason != "" {
		identity.UnavailableReason = reason
	}
	return identity, repository.GitDir, false
}

func resolveCatalogCheckout(ctx context.Context, checkout *catalogCheckout) string {
	root := checkout.Path
	branch, err := project.Git(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		checkout.Branch = branch
		if upstream, upstreamErr := project.Git(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); upstreamErr == nil {
			checkout.IsMain = strings.TrimPrefix(upstream, "origin/") == branch
		} else {
			checkout.IsMain = branch == "main" || branch == "master"
		}
	} else if head, headErr := project.Git(ctx, root, "rev-parse", "HEAD"); headErr == nil {
		checkout.DetachedHead = head
	} else {
		return "repository head is unavailable"
	}
	porcelain, err := project.Git(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "repository status is unavailable"
	}
	checkout.Dirty = porcelain != ""
	return ""
}
