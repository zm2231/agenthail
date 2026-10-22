package daemon

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
	catalogProjectGit    = "git"
	catalogProjectFolder = "folder"
)

// catalogProject is the project a session belongs to. ID is Key when the
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
	Project           *CatalogProject `json:"project,omitempty"`
	Checkout          catalogCheckout `json:"checkout"`
	UnavailableReason string          `json:"unavailableReason,omitempty"`
}

// catalogIdentityTTL bounds how long a resolved workspace identity is reused.
// A change to the checkout's HEAD or index invalidates it sooner; working
// tree edits that touch neither show up in Dirty once the entry expires.
const catalogIdentityTTL = 2 * time.Minute

const catalogIdentityGitTimeout = 3 * time.Second

// catalogIdentityRefreshes bounds the git work re-resolving expired
// identities in the background at once.
const catalogIdentityRefreshes = 4

// catalogIdentityCache resolves each workspace path once, then serves it
// while at most one background refresh per path re-reads an entry that
// expired or whose HEAD or index changed. A discovery pass runs git only for
// a path it has never seen or whose repository config changed.
type catalogIdentityCache struct {
	mu      sync.Mutex
	entries map[string]catalogIdentityEntry
	// known keeps the last resolved project per path past the TTL, so a git
	// failure that is not "not a repository" never moves a session.
	known        map[string]*CatalogProject
	projects     []registry.ProjectRecord
	generation   uint64
	repositories *catalogRepositoryCache
	roots        map[string]catalogRootEntry
	refreshing   map[string]struct{}
	slots        chan struct{}
	inflight     sync.WaitGroup
	// changed runs after a background refresh changed a path's identity.
	changed func()
}

type catalogRootEntry struct {
	key        string
	repository []byte
	gitDirs    catalogGitDirs
	gitStamp   string
	resolvedAt time.Time
}

type catalogIdentityEntry struct {
	identity catalogIdentity
	gitDirs  catalogGitDirs
	gitStamp string
	// configStamp fingerprints the repository config alone: a remote change
	// moves the session to another project, so it re-resolves at once.
	configStamp string
	resolvedAt  time.Time
	usedAt      time.Time
}

func newCatalogIdentityCache() *catalogIdentityCache {
	return &catalogIdentityCache{entries: map[string]catalogIdentityEntry{}, known: map[string]*CatalogProject{}, repositories: &catalogRepositoryCache{entries: map[string]catalogRepositoryEntry{}}, roots: map[string]catalogRootEntry{}, refreshing: map[string]struct{}{}, slots: make(chan struct{}, catalogIdentityRefreshes)}
}

// wait returns once no background refresh is running.
func (c *catalogIdentityCache) wait() { c.inflight.Wait() }

// setProjects replaces the added projects. A changed set drops the cached
// identities, since a path may now belong to another project.
func (c *catalogIdentityCache) setProjects(records []registry.ProjectRecord) {
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

// rootRepository reads the key and repository of an added project's folder.
// ok is false when git failed for a reason other than the folder being
// outside a repository, so the stored key is kept.
func (c *catalogIdentityCache) rootRepository(ctx context.Context, root string, now time.Time) (key string, repository []byte, ok bool) {
	c.mu.Lock()
	entry, found := c.roots[root]
	c.mu.Unlock()
	if found && now.Sub(entry.resolvedAt) < catalogIdentityTTL && catalogGitStamp(entry.gitDirs) == entry.gitStamp {
		return entry.key, entry.repository, true
	}
	resolved, notRepository, err := resolveCatalogRepository(ctx, root, c.repositories)
	if err != nil && !notRepository {
		return "", nil, false
	}
	var dirs catalogGitDirs
	if err == nil {
		dirs.common = resolved.commonDir
		key = resolved.key(root)
		if key != "" {
			repository, _ = json.Marshal(resolved.repository)
		}
	}
	c.mu.Lock()
	c.roots[root] = catalogRootEntry{key: key, repository: repository, gitDirs: dirs, gitStamp: catalogGitStamp(dirs), resolvedAt: now}
	c.mu.Unlock()
	return key, repository, true
}

// identity resolves a session's project and checkout. prior reads the project
// last stored for the session; it is used when git fails and this cache has
// never resolved the path.
func (c *catalogIdentityCache) identity(ctx context.Context, session surface.Session, now time.Time, prior func() *CatalogProject) catalogIdentity {
	path, err := catalogSessionPath(session.Cwd)
	if err != nil || path == "" {
		return catalogIdentity{Project: prior(), UnavailableReason: "workspace path is unavailable"}
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
	if now.Sub(entry.resolvedAt) >= catalogIdentityTTL || catalogGitStamp(entry.gitDirs) != entry.gitStamp {
		c.refreshLater(ctx, path, now, prior)
	}
	return entry.identity
}

// resolve reads path's identity with git and caches it, a failed read too,
// so a workspace whose git fails is retried once per TTL rather than on
// every discovery pass.
func (c *catalogIdentityCache) resolve(ctx context.Context, path string, now time.Time, prior func() *CatalogProject) catalogIdentity {
	c.mu.Lock()
	projects, generation := c.projects, c.generation
	c.mu.Unlock()
	identity, gitDirs, transient := resolveCatalogIdentity(ctx, path, projects, c.repositories)
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
// already running or every refresh slot is taken; a later pass retries.
func (c *catalogIdentityCache) refreshLater(ctx context.Context, path string, now time.Time, prior func() *CatalogProject) {
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
		after := c.resolve(context.WithoutCancel(ctx), path, now, prior)
		c.mu.Lock()
		delete(c.refreshing, path)
		<-c.slots
		changed := c.changed
		c.mu.Unlock()
		if changed != nil && !reflect.DeepEqual(before, after) {
			changed()
		}
	}()
}

// prune forgets the paths no listed session asked for within the TTL.
func (c *catalogIdentityCache) prune(now time.Time) {
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

// catalogGitStamp fingerprints the files that change on a branch switch,
// commit or staging change, and the repository config, which holds the
// remote the project key comes from.
func catalogGitStamp(dirs catalogGitDirs) string {
	var stamp strings.Builder
	for _, file := range []struct{ dir, name string }{{dirs.checkout, "HEAD"}, {dirs.checkout, "index"}, {dirs.common, "config"}} {
		if file.dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(file.dir, file.name)); err == nil {
			fmt.Fprintf(&stamp, "%s:%d:%d;", file.name, info.ModTime().UnixNano(), info.Size())
		} else {
			fmt.Fprintf(&stamp, "%s:missing;", file.name)
		}
	}
	return stamp.String()
}

// catalogConfigStamp fingerprints the repository config, which holds the
// remote the project key comes from.
func catalogConfigStamp(dirs catalogGitDirs) string {
	return catalogGitStamp(catalogGitDirs{common: dirs.common})
}

// resolveCatalogIdentity returns the workspace's identity, the git
// directories it was read from (empty outside a repository), and whether the project could not
// be resolved for a reason other than the path not being in a repository.
//
// A session belongs to the deepest added project containing its folder, or
// the same place in the repository's main checkout, so a linked worktree's
// session finds the projects added in the main checkout. Without one, a
// repository is an implicit project at its main checkout.
func resolveCatalogIdentity(ctx context.Context, path string, projects []registry.ProjectRecord, repositories *catalogRepositoryCache) (catalogIdentity, catalogGitDirs, bool) {
	identity := catalogIdentity{Checkout: catalogCheckout{ID: catalogPathID("checkout", path), Path: path}}
	existing := nearestExistingDirectory(path)
	repository, notRepository, err := resolveCatalogRepository(ctx, existing, repositories)
	if err != nil && !notRepository {
		identity.UnavailableReason = "repository identity is unavailable"
		return identity, catalogGitDirs{}, true
	}
	inRepository := err == nil
	candidates := []string{path}
	if inRepository {
		candidates = append(candidates, repository.translate(path))
	}
	if added := deepestCatalogProject(projects, candidates); added != nil {
		identity.Project = addedCatalogProject(*added, inRepository && catalogWithin(repository.mainRoot, added.WorkspaceRoot))
	} else if inRepository {
		identity.Project = repository.implicitProject()
	}
	if !inRepository {
		return identity, catalogGitDirs{}, false
	}
	if existing != path || repository.bare {
		return identity, catalogGitDirs{common: repository.commonDir}, false
	}
	identity.Checkout.Path = repository.toplevel
	identity.Checkout.ID = catalogPathID("checkout", repository.toplevel)
	checkoutCtx, cancel := context.WithTimeout(ctx, catalogIdentityGitTimeout)
	defer cancel()
	if reason := resolveCatalogCheckout(checkoutCtx, &identity.Checkout); reason != "" {
		identity.UnavailableReason = reason
	}
	return identity, catalogGitDirs{checkout: repository.gitDir, common: repository.commonDir}, false
}

type catalogRepository struct {
	bare       bool
	toplevel   string
	commonDir  string
	gitDir     string
	mainRoot   string
	repository *project.Repository
}

// translate maps a folder in this checkout to the same folder in the main
// checkout.
func (repository catalogRepository) translate(path string) string {
	if !catalogWithin(repository.toplevel, path) {
		return path
	}
	rel, err := filepath.Rel(repository.toplevel, path)
	if err != nil {
		return path
	}
	return filepath.Join(repository.mainRoot, rel)
}

// key names root, a folder in the main checkout, on every host. A repository
// without a remote has no key.
func (repository catalogRepository) key(root string) string {
	if repository.repository == nil || !catalogWithin(repository.mainRoot, root) {
		return ""
	}
	rel, err := filepath.Rel(repository.mainRoot, root)
	if err != nil {
		return ""
	}
	return project.Key(repository.repository.CanonicalKey, filepath.ToSlash(rel))
}

func (repository catalogRepository) implicitProject() *CatalogProject {
	name := catalogProjectName(repository.mainRoot)
	if repository.repository != nil {
		name = repository.repository.Name
	}
	found := &CatalogProject{LocalID: catalogImplicitProjectID(repository.mainRoot), DisplayName: name, RootPath: repository.mainRoot, Kind: catalogProjectGit, Key: repository.key(repository.mainRoot), Repository: repository.repository}
	found.ID = found.LocalID
	if found.Key != "" {
		found.ID = found.Key
	}
	return found
}

func catalogImplicitProjectID(mainRoot string) string { return "implicit:" + mainRoot }

func addedCatalogProject(record registry.ProjectRecord, inRepository bool) *CatalogProject {
	found := &CatalogProject{ID: record.ID, LocalID: record.ID, DisplayName: record.Title, RootPath: record.WorkspaceRoot, Kind: catalogProjectFolder, Added: true}
	if inRepository {
		found.Kind = catalogProjectGit
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

// catalogRepositoryCache holds each repository's identity by its common
// directory until the repository's config changes. A nil cache reads every
// time.
type catalogRepositoryCache struct {
	mu      sync.Mutex
	entries map[string]catalogRepositoryEntry
}

type catalogRepositoryEntry struct {
	repository *project.Repository
	stamp      string
}

func (repositories *catalogRepositoryCache) repository(ctx context.Context, commonDir string) (*project.Repository, error) {
	stamp := "missing"
	if info, err := os.Stat(filepath.Join(commonDir, "config")); err == nil {
		stamp = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	}
	if repositories != nil {
		repositories.mu.Lock()
		entry, found := repositories.entries[commonDir]
		repositories.mu.Unlock()
		if found && entry.stamp == stamp {
			return entry.repository, nil
		}
	}
	repository, err := readCatalogRepository(ctx, commonDir)
	if err != nil {
		return nil, err
	}
	if repositories != nil {
		repositories.mu.Lock()
		repositories.entries[commonDir] = catalogRepositoryEntry{repository: repository, stamp: stamp}
		repositories.mu.Unlock()
	}
	return repository, nil
}

// readCatalogRepository reads the remote that names the repository: origin,
// else the first remote by name. It is nil when no remote names one.
func readCatalogRepository(ctx context.Context, commonDir string) (*project.Repository, error) {
	output, err := catalogGit(ctx, commonDir, "config", "--null", "--get-regexp", `^remote\..*\.url$`)
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

// resolveCatalogRepository reads the checkout root, the shared repository
// directory and the checkout's own git directory. Each is its own git call
// because a path may contain a newline. The main root is the main
// checkout: the parent of a ".git" common directory, the checkout itself
// when it owns its git directory (a submodule), and the common directory of
// a bare repository.
func resolveCatalogRepository(ctx context.Context, path string, repositories *catalogRepositoryCache) (repository catalogRepository, notRepository bool, err error) {
	gitCtx, cancel := context.WithTimeout(ctx, catalogIdentityGitTimeout)
	defer cancel()
	toplevel, err := catalogGit(gitCtx, path, "rev-parse", "--show-toplevel")
	if err != nil && isCatalogBare(gitCtx, path, err) {
		repository.bare = true
		toplevel, err = catalogGit(gitCtx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	}
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
		repository.mainRoot = filepath.Dir(repository.commonDir)
	case repository.gitDir == repository.commonDir:
		repository.mainRoot = repository.toplevel
	default:
		repository.mainRoot = repository.commonDir
	}
	if repository.repository, err = repositories.repository(gitCtx, repository.commonDir); err != nil {
		return catalogRepository{}, false, err
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
	bare, bareErr := catalogGit(ctx, path, "rev-parse", "--is-bare-repository")
	return bareErr == nil && bare == "true"
}

// isCatalogNonGit reports git's answer that the folder is outside any
// repository. Other failures, such as unreadable metadata or a dubious
// ownership refusal, are transient for identity and keep the last project.
func isCatalogNonGit(err error) bool {
	var gitErr *catalogGitError
	return errors.As(err, &gitErr) && strings.Contains(gitErr.stderr, "not a git repository")
}
