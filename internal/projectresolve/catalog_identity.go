package projectresolve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/project"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

const (
	CatalogProjectGit    = "git"
	CatalogProjectFolder = "folder"
)

// CatalogProject is the project a session belongs to. ID is Key when the
// project has one, so every host's checkout of it is one project; otherwise
// it is LocalID, this host's own id for it.
type CatalogProject struct {
	ID          string              `json:"id"`
	LocalID     string              `json:"localId"`
	DisplayName string              `json:"displayName"`
	RootPath    string              `json:"rootPath"`
	Kind        string              `json:"kind"`
	Added       bool                `json:"added,omitempty"`
	Key         string              `json:"key,omitempty"`
	Repository  *project.Repository `json:"repository,omitempty"`
	Archived    bool                `json:"archived,omitempty"`
	RootCommits []string            `json:"rootCommits,omitempty"`
	// LinkedFrom is the project this checkout belongs to before a link joins
	// it into ID.
	LinkedFrom *CatalogProjectRef `json:"linkedFrom,omitempty"`
}

type CatalogProjectRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type CatalogCheckout struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	DetachedHead string `json:"detachedHead,omitempty"`
	IsMain       bool   `json:"isMain"`
	Dirty        bool   `json:"dirty"`
}

type CatalogIdentity struct {
	Project  *CatalogProject
	Checkout CatalogCheckout
	Problem  string
}

// catalogIdentityTTL bounds how long a resolved workspace identity is reused.
// A change to the git state it was read from invalidates it sooner; working
// tree edits that touch none of it show up in Dirty once the entry expires.
const catalogIdentityTTL = 2 * time.Minute

const CatalogIdentityGitTimeout = 3 * time.Second

// catalogIdentityRefreshes bounds the git work re-resolving expired
// identities in the background at once.
const catalogIdentityRefreshes = 4

// CatalogIdentityCache resolves each workspace path once, then serves it
// while at most one background refresh per path re-reads an entry that
// expired or whose HEAD or index changed. A discovery pass runs git only for
// a path it has never seen or whose repository config changed.
type CatalogIdentityCache struct {
	mu      sync.Mutex
	entries map[string]catalogIdentityEntry
	// known keeps the last resolved project per path past the TTL, so a git
	// failure that is not "not a repository" never moves a session.
	known        map[string]*CatalogProject
	projects     []registry.ProjectRecord
	links        ProjectLinkSet
	generation   uint64
	repositories *CatalogRepositoryCache
	roots        map[string]catalogRootEntry
	refreshing   map[string]struct{}
	slots        chan struct{}
	inflight     sync.WaitGroup
	// Changed runs after a background refresh changed a path's identity.
	Changed     func()
	ObserveRoot func(root, key string)
}

type catalogRootEntry struct {
	key        string
	repository []byte
	gitDirs    catalogGitDirs
	gitStamp   string
	resolvedAt time.Time
}

type catalogIdentityEntry struct {
	identity CatalogIdentity
	gitDirs  catalogGitDirs
	gitStamp string
	// configStamp fingerprints the repository config alone: a remote change
	// moves the session to another project, so it re-resolves at once.
	configStamp string
	resolvedAt  time.Time
	usedAt      time.Time
}

func NewCatalogIdentityCache() *CatalogIdentityCache {
	return &CatalogIdentityCache{entries: map[string]catalogIdentityEntry{}, known: map[string]*CatalogProject{}, repositories: &CatalogRepositoryCache{entries: map[string]catalogRepositoryEntry{}}, roots: map[string]catalogRootEntry{}, refreshing: map[string]struct{}{}, slots: make(chan struct{}, catalogIdentityRefreshes)}
}

// Wait returns once no background refresh is running.
func (c *CatalogIdentityCache) Wait() { c.inflight.Wait() }

// SetProjects replaces the added projects. A changed set drops the cached
// identities, since a path may now belong to another project.
func (c *CatalogIdentityCache) SetProjects(records []registry.ProjectRecord) {
	added := make([]registry.ProjectRecord, 0, len(records))
	for _, record := range records {
		if record.Added {
			added = append(added, record)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if slices.EqualFunc(c.projects, added, func(left, right registry.ProjectRecord) bool {
		return left.ID == right.ID && left.WorkspaceRoot == right.WorkspaceRoot && left.Title == right.Title && left.Key == right.Key
	}) {
		return
	}
	c.projects = added
	c.generation++
	c.entries = map[string]catalogIdentityEntry{}
}

func (c *CatalogIdentityCache) SetLinks(links ProjectLinkSet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.links.equal(links) {
		c.links = links
		return
	}
	c.links = links
	c.generation++
	c.entries = map[string]catalogIdentityEntry{}
	c.known = map[string]*CatalogProject{}
}

// RootRepository reads the key and repository of an added project's folder.
// ok is false when git failed for a reason other than the folder being
// outside a repository, so the stored key is kept.
func (c *CatalogIdentityCache) RootRepository(ctx context.Context, root string, now time.Time) (key string, repository []byte, ok bool) {
	c.mu.Lock()
	entry, found := c.roots[root]
	c.mu.Unlock()
	if found && now.Sub(entry.resolvedAt) < catalogIdentityTTL && catalogConfigStamp(entry.gitDirs) == entry.gitStamp {
		return entry.key, entry.repository, true
	}
	resolved, notRepository, err := ResolveCatalogRepository(ctx, root, c.repositories)
	if err != nil && !notRepository {
		return "", nil, false
	}
	var dirs catalogGitDirs
	if err == nil {
		dirs.common = resolved.commonDir
		key = resolved.Key(root)
		if key != "" {
			repository, _ = json.Marshal(resolved.Repository)
		}
	}
	c.mu.Lock()
	c.roots[root] = catalogRootEntry{key: key, repository: repository, gitDirs: dirs, gitStamp: catalogConfigStamp(dirs), resolvedAt: now}
	c.mu.Unlock()
	return key, repository, true
}

// Identity resolves a session's project and checkout. prior reads the project
// last stored for the session; it is used when git fails and this cache has
// never resolved the path.
func (c *CatalogIdentityCache) Identity(ctx context.Context, session surface.Session, now time.Time, prior func() *CatalogProject) CatalogIdentity {
	path, err := catalogSessionPath(session.Cwd)
	if err != nil || path == "" {
		return CatalogIdentity{Project: prior(), Problem: "workspace path is unavailable"}
	}
	c.mu.Lock()
	entry, found := c.entries[path]
	if found {
		entry.usedAt = now
		c.entries[path] = entry
	}
	c.mu.Unlock()
	if !found || catalogConfigStamp(entry.gitDirs) != entry.configStamp {
		return c.resolve(ctx, path, now, prior)
	}
	moved := catalogGitStamp(entry.gitDirs) != entry.gitStamp
	if moved || now.Sub(entry.resolvedAt) >= catalogIdentityTTL {
		c.refreshLater(ctx, path, now, prior, !moved && entry.gitDirs.checkout != "" && entry.identity.Problem == "")
	}
	return entry.identity
}

// resolve reads path's identity with git and caches it, a failed read too,
// so a workspace whose git fails is retried once per TTL rather than on
// every discovery pass.
func (c *CatalogIdentityCache) resolve(ctx context.Context, path string, now time.Time, prior func() *CatalogProject) CatalogIdentity {
	c.mu.Lock()
	projects, links, generation := c.projects, c.links, c.generation
	c.mu.Unlock()
	identity, gitDirs, transient := resolveUnlinkedCatalogIdentity(ctx, path, projects, c.repositories)
	if project := identity.Project; !transient && project != nil && !project.Added && project.Key != "" && c.ObserveRoot != nil {
		c.ObserveRoot(project.RootPath, project.Key)
	}
	identity.Project = links.Apply(identity.Project)
	if transient {
		c.mu.Lock()
		known, found := c.known[path]
		c.mu.Unlock()
		if found {
			identity.Project = known
		} else {
			identity.Project = prior()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return identity
	}
	if !transient {
		c.known[path] = identity.Project
	}
	c.entries[path] = catalogIdentityEntry{identity: identity, gitDirs: gitDirs, gitStamp: catalogGitStamp(gitDirs), configStamp: catalogConfigStamp(gitDirs), resolvedAt: now, usedAt: now}
	return identity
}

// refreshLater re-resolves path in the background unless a refresh of it is
// already running or every refresh slot is taken; a later pass retries. With
// dirtyOnly, git state the stamp covers has not moved, so only Dirty is read.
func (c *CatalogIdentityCache) refreshLater(ctx context.Context, path string, now time.Time, prior func() *CatalogProject, dirtyOnly bool) {
	c.mu.Lock()
	if _, running := c.refreshing[path]; running {
		c.mu.Unlock()
		return
	}
	select {
	case c.slots <- struct{}{}:
	default:
		c.mu.Unlock()
		return
	}
	c.refreshing[path] = struct{}{}
	before := c.entries[path].identity
	c.inflight.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.inflight.Done()
		after, refreshed := CatalogIdentity{}, false
		if dirtyOnly {
			after, refreshed = c.refreshDirty(context.WithoutCancel(ctx), path, now)
		}
		if !refreshed {
			after = c.resolve(context.WithoutCancel(ctx), path, now, prior)
		}
		c.mu.Lock()
		delete(c.refreshing, path)
		<-c.slots
		changed := c.Changed
		c.mu.Unlock()
		if changed != nil && !reflect.DeepEqual(before, after) {
			changed()
		}
	}()
}

// refreshDirty rereads whether path's checkout is dirty and renews its entry;
// false when anything else may have changed, so path is resolved in full.
func (c *CatalogIdentityCache) refreshDirty(ctx context.Context, path string, now time.Time) (CatalogIdentity, bool) {
	c.mu.Lock()
	entry, found := c.entries[path]
	generation := c.generation
	c.mu.Unlock()
	stamp := catalogGitStamp(entry.gitDirs)
	if !found || stamp != entry.gitStamp || catalogNestedRepository(path, entry.identity.Checkout.Path) {
		return CatalogIdentity{}, false
	}
	gitCtx, cancel := context.WithTimeout(ctx, CatalogIdentityGitTimeout)
	defer cancel()
	dirty, err := catalogCheckoutDirty(gitCtx, entry.identity.Checkout.Path)
	if err != nil {
		return CatalogIdentity{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, found := c.entries[path]
	if !found || generation != c.generation || current.resolvedAt != entry.resolvedAt {
		return current.identity, found
	}
	current.identity.Checkout.Dirty = dirty
	current.gitStamp, current.resolvedAt = stamp, now
	c.entries[path] = current
	return current.identity, true
}

// catalogNestedRepository reports a .git between path and its checkout root,
// which makes path part of another repository from the one it resolved to.
func catalogNestedRepository(path, root string) bool {
	inside := root
	if !strings.HasSuffix(inside, string(filepath.Separator)) {
		inside += string(filepath.Separator)
	}
	for dir := path; dir != root && strings.HasPrefix(dir, inside); dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
	}
	return false
}

// Prune forgets the paths no listed session asked for within the TTL.
func (c *CatalogIdentityCache) Prune(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, entry := range c.entries {
		if now.Sub(entry.usedAt) >= catalogIdentityTTL {
			delete(c.entries, path)
		}
	}
	for root, entry := range c.roots {
		if now.Sub(entry.resolvedAt) >= catalogIdentityTTL {
			delete(c.roots, root)
		}
	}
}

// catalogGitDirs names the git state an identity was read from: the
// checkout's own git directory, when the identity includes its checkout, and
// the repository's common directory.
type catalogGitDirs struct {
	checkout string
	common   string
}

// catalogGitStamp fingerprints every file an identity's git reads depend on:
// HEAD for the branch or detached commit, the index, the config holding the
// remotes, and origin's HEAD, which decides whether the branch is the main one.
func catalogGitStamp(dirs catalogGitDirs) string {
	return catalogFileStamp(catalogGitFile{dirs.checkout, "HEAD"}, catalogGitFile{dirs.checkout, "index"}, catalogGitFile{dirs.common, "config"}, catalogGitFile{dirs.common, filepath.Join("refs", "remotes", "origin", "HEAD")})
}

// catalogConfigStamp fingerprints the repository config, which holds the
// remote the project key comes from.
func catalogConfigStamp(dirs catalogGitDirs) string {
	return catalogFileStamp(catalogGitFile{dirs.common, "config"})
}

type catalogGitFile struct{ dir, name string }

func catalogFileStamp(files ...catalogGitFile) string {
	readAt := time.Now()
	var stamp strings.Builder
	for _, file := range files {
		if file.dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(file.dir, file.name)); err == nil {
			fmt.Fprintf(&stamp, "%s:%s;", file.name, surface.SettledStamp(info, readAt))
		} else {
			fmt.Fprintf(&stamp, "%s:missing;", file.name)
		}
	}
	return stamp.String()
}

// resolveCatalogIdentity returns the workspace's identity, the git
// directories it was read from (empty outside a repository), and whether the project could not
// be resolved for a reason other than the path not being in a repository.
//
// A session belongs to the deepest added project containing its folder, or
// the same place in the repository's main checkout, so a linked worktree's
// session finds the projects added in the main checkout. Without one, a
// repository is an implicit project at its main checkout.
func resolveCatalogIdentity(ctx context.Context, path string, projects []registry.ProjectRecord, links ProjectLinkSet, repositories *CatalogRepositoryCache) (CatalogIdentity, catalogGitDirs, bool) {
	identity, dirs, transient := resolveUnlinkedCatalogIdentity(ctx, path, projects, repositories)
	identity.Project = links.Apply(identity.Project)
	return identity, dirs, transient
}

func resolveUnlinkedCatalogIdentity(ctx context.Context, path string, projects []registry.ProjectRecord, repositories *CatalogRepositoryCache) (CatalogIdentity, catalogGitDirs, bool) {
	identity := CatalogIdentity{Checkout: CatalogCheckout{ID: catalogPathID("checkout", path), Path: path}}
	existing := nearestExistingDirectory(path)
	repository, notRepository, err := ResolveCatalogRepository(ctx, existing, repositories)
	if err != nil && !notRepository {
		identity.Problem = "repository identity is unavailable"
		return identity, catalogGitDirs{}, true
	}
	inRepository := err == nil
	candidates := []string{path}
	if inRepository {
		candidates = append(candidates, repository.Translate(path))
	}
	if added := deepestCatalogProject(projects, candidates); added != nil {
		identity.Project = AddedCatalogProject(*added, inRepository && catalogWithin(repository.MainRoot, added.WorkspaceRoot))
	} else if inRepository {
		identity.Project = repository.implicitProject()
	}
	if !inRepository {
		return identity, catalogGitDirs{}, false
	}
	if existing != path || repository.Bare {
		return identity, catalogGitDirs{common: repository.commonDir}, false
	}
	identity.Checkout.Path = repository.Toplevel
	identity.Checkout.ID = catalogPathID("checkout", repository.Toplevel)
	checkoutCtx, cancel := context.WithTimeout(ctx, CatalogIdentityGitTimeout)
	defer cancel()
	if reason := resolveCatalogCheckout(checkoutCtx, &identity.Checkout); reason != "" {
		identity.Problem = reason
	}
	return identity, catalogGitDirs{checkout: repository.gitDir, common: repository.commonDir}, false
}

type CatalogRepository struct {
	Bare        bool
	Toplevel    string
	commonDir   string
	gitDir      string
	MainRoot    string
	Repository  *project.Repository
	rootCommits []string
}

// Translate maps a folder in this checkout to the same folder in the main
// checkout.
func (repository CatalogRepository) Translate(path string) string {
	if !catalogWithin(repository.Toplevel, path) {
		return path
	}
	rel, err := filepath.Rel(repository.Toplevel, path)
	if err != nil {
		return path
	}
	return filepath.Join(repository.MainRoot, rel)
}

// Key names root, a folder in the main checkout, on every host. A repository
// without a remote has no key.
func (repository CatalogRepository) Key(root string) string {
	if repository.Repository == nil || !catalogWithin(repository.MainRoot, root) {
		return ""
	}
	rel, err := filepath.Rel(repository.MainRoot, root)
	if err != nil {
		return ""
	}
	return project.Key(repository.Repository.CanonicalKey, filepath.ToSlash(rel))
}

func (repository CatalogRepository) implicitProject() *CatalogProject {
	name := CatalogProjectName(repository.MainRoot)
	if repository.Repository != nil {
		name = repository.Repository.Name
	}
	found := &CatalogProject{LocalID: CatalogImplicitProjectID(repository.MainRoot), DisplayName: name, RootPath: repository.MainRoot, Kind: CatalogProjectGit, Key: repository.Key(repository.MainRoot), Repository: repository.Repository, RootCommits: repository.rootCommits}
	found.ID = found.LocalID
	if found.Key != "" {
		found.ID = found.Key
	}
	return found
}

func CatalogImplicitProjectID(mainRoot string) string { return "implicit:" + mainRoot }

func AddedCatalogProject(record registry.ProjectRecord, inRepository bool) *CatalogProject {
	found := &CatalogProject{ID: record.ID, LocalID: record.ID, DisplayName: record.Title, RootPath: record.WorkspaceRoot, Kind: CatalogProjectFolder, Added: true}
	if inRepository {
		found.Kind = CatalogProjectGit
	}
	if record.Key != "" {
		found.ID, found.Key = record.Key, record.Key
		var repository project.Repository
		if json.Unmarshal(record.Repository, &repository) == nil && repository.CanonicalKey != "" {
			found.Repository = &repository
		}
	}
	return found
}

func deepestCatalogProject(projects []registry.ProjectRecord, paths []string) *registry.ProjectRecord {
	var deepest *registry.ProjectRecord
	for index := range projects {
		for _, path := range paths {
			if catalogWithin(projects[index].WorkspaceRoot, path) && (deepest == nil || len(projects[index].WorkspaceRoot) > len(deepest.WorkspaceRoot)) {
				deepest = &projects[index]
			}
		}
	}
	return deepest
}

func catalogWithin(root, path string) bool {
	within, err := workspace.IsWithin(root, path)
	return err == nil && within
}

// CatalogRepositoryCache holds each repository's identity by its common
// directory until the repository's config changes. A nil cache reads every
// time.
type CatalogRepositoryCache struct {
	mu      sync.Mutex
	entries map[string]catalogRepositoryEntry
}

type catalogRepositoryEntry struct {
	repository  *project.Repository
	rootCommits []string
	stamp       string
}

// A repository without commits yet is read again next time, so its first
// commit's root is found without waiting for a config change.
func (repositories *CatalogRepositoryCache) repository(ctx context.Context, commonDir string) (*project.Repository, []string, error) {
	stamp := "missing"
	if info, err := os.Stat(filepath.Join(commonDir, "config")); err == nil {
		stamp = surface.SettledStamp(info, time.Now())
	}
	if repositories != nil {
		repositories.mu.Lock()
		entry, found := repositories.entries[commonDir]
		repositories.mu.Unlock()
		if found && entry.stamp == stamp && len(entry.rootCommits) > 0 {
			return entry.repository, entry.rootCommits, nil
		}
	}
	repository, err := readCatalogRepository(ctx, commonDir)
	if err != nil {
		return nil, nil, err
	}
	roots := readCatalogRootCommits(ctx, commonDir)
	if repositories != nil {
		repositories.mu.Lock()
		repositories.entries[commonDir] = catalogRepositoryEntry{repository: repository, rootCommits: roots, stamp: stamp}
		repositories.mu.Unlock()
	}
	return repository, roots, nil
}

func readCatalogRootCommits(ctx context.Context, commonDir string) []string {
	output, err := CatalogGit(ctx, commonDir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return nil
	}
	roots := strings.Fields(output)
	slices.Sort(roots)
	return roots
}

// readCatalogRepository reads the remote that names the repository: origin,
// else the first remote by name. It is nil when no remote names one.
func readCatalogRepository(ctx context.Context, commonDir string) (*project.Repository, error) {
	output, err := CatalogGit(ctx, commonDir, "config", "--null", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		if isCatalogNoMatch(err) {
			return nil, nil
		}
		return nil, err
	}
	urls := map[string]string{}
	for _, entry := range strings.Split(output, "\x00") {
		name, value, found := strings.Cut(entry, "\n")
		if !found {
			continue
		}
		name = strings.TrimSuffix(strings.TrimPrefix(name, "remote."), ".url")
		if _, seen := urls[name]; !seen {
			urls[name] = value
		}
	}
	repository, ok := project.PickRemote(urls)
	if !ok {
		return nil, nil
	}
	return &repository, nil
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

// ResolveCatalogRepository reads the checkout root, the shared repository
// directory and the checkout's own git directory. Each is its own git call
// because a path may contain a newline. The main root is the main
// checkout: the parent of a ".git" common directory, the checkout itself
// when it owns its git directory (a submodule), and the common directory of
// a bare repository.
func ResolveCatalogRepository(ctx context.Context, path string, repositories *CatalogRepositoryCache) (repository CatalogRepository, notRepository bool, err error) {
	gitCtx, cancel := context.WithTimeout(ctx, CatalogIdentityGitTimeout)
	defer cancel()
	toplevel, err := CatalogGit(gitCtx, path, "rev-parse", "--show-toplevel")
	if err != nil && isCatalogBare(gitCtx, path, err) {
		repository.Bare = true
		toplevel, err = CatalogGit(gitCtx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	}
	if err != nil {
		return CatalogRepository{}, isCatalogNonGit(err), err
	}
	if repository.Toplevel, err = CanonicalCatalogPath(toplevel); err != nil {
		return CatalogRepository{}, false, err
	}
	for _, read := range []struct {
		target *string
		args   []string
	}{
		{&repository.commonDir, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}},
		{&repository.gitDir, []string{"rev-parse", "--absolute-git-dir"}},
	} {
		value, err := CatalogGit(gitCtx, repository.Toplevel, read.args...)
		if err != nil {
			return CatalogRepository{}, false, err
		}
		if *read.target, err = CanonicalCatalogPath(value); err != nil {
			return CatalogRepository{}, false, err
		}
	}
	switch {
	case filepath.Base(repository.commonDir) == ".git":
		repository.MainRoot = filepath.Dir(repository.commonDir)
	case repository.gitDir == repository.commonDir:
		repository.MainRoot = repository.Toplevel
	default:
		repository.MainRoot = repository.commonDir
	}
	if repository.Repository, repository.rootCommits, err = repositories.repository(gitCtx, repository.commonDir); err != nil {
		return CatalogRepository{}, false, err
	}
	return repository, false, nil
}

func resolveCatalogCheckout(ctx context.Context, checkout *CatalogCheckout) string {
	root := checkout.Path
	branch, err := CatalogGit(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil {
		checkout.Branch = branch
		if upstream, upstreamErr := CatalogGit(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); upstreamErr == nil {
			checkout.IsMain = strings.TrimPrefix(upstream, "origin/") == branch
		} else {
			checkout.IsMain = branch == "main" || branch == "master"
		}
	} else if head, headErr := CatalogGit(ctx, root, "rev-parse", "HEAD"); headErr == nil {
		checkout.DetachedHead = head
	} else {
		return "repository head is unavailable"
	}
	dirty, err := catalogCheckoutDirty(ctx, root)
	if err != nil {
		return "repository status is unavailable"
	}
	checkout.Dirty = dirty
	return ""
}

func catalogCheckoutDirty(ctx context.Context, root string) (bool, error) {
	porcelain, err := CatalogGit(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	return porcelain != "", err
}

func CatalogProjectName(root string) string {
	name := filepath.Base(root)
	if trimmed := strings.TrimSuffix(name, ".git"); trimmed != "" {
		return trimmed
	}
	return name
}

func CatalogGit(ctx context.Context, dir string, args ...string) (string, error) {
	command := GitCommand(ctx, dir, args...)
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

func CanonicalCatalogPath(path string) (string, error) {
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

// isCatalogNoMatch reports "git config --get-regexp" finding no entry, which
// it says with exit status 1 and nothing on stderr.
func isCatalogNoMatch(err error) bool {
	var gitErr *catalogGitError
	var exitErr *exec.ExitError
	return errors.As(err, &gitErr) && strings.TrimSpace(gitErr.stderr) == "" && errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}

// isCatalogBare reports that git refused --show-toplevel because path is in
// a bare repository, which has no work tree; its common directory stands in
// for the checkout root.
func isCatalogBare(ctx context.Context, path string, err error) bool {
	var gitErr *catalogGitError
	if !errors.As(err, &gitErr) || !strings.Contains(gitErr.stderr, "must be run in a work tree") {
		return false
	}
	bare, bareErr := CatalogGit(ctx, path, "rev-parse", "--is-bare-repository")
	return bareErr == nil && bare == "true"
}

// isCatalogNonGit reports git's answer that the folder is outside any
// repository. Other failures, such as unreadable metadata or a dubious
// ownership refusal, are transient for identity and keep the last project.
func isCatalogNonGit(err error) bool {
	var gitErr *catalogGitError
	return errors.As(err, &gitErr) && strings.Contains(gitErr.stderr, "not a git repository")
}
