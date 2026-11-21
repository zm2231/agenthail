package surface

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

type claudeRootsCache struct {
	mu      sync.Mutex
	entries map[string]claudeRootsEntry
	resolve func(home string) ([]claudeRootSource, []string, error)
}

type claudeRootsEntry struct {
	read    time.Time
	files   []pathStamp
	roots   []pathStamp
	sources []claudeRootSource
}

type pathStamp struct {
	path string
	stat func(string) (os.FileInfo, error)
	info os.FileInfo
}

var claudeRoots = &claudeRootsCache{entries: map[string]claudeRootsEntry{}, resolve: resolveClaudeConfigRootSources}

func (c *claudeRootsCache) sources(home string) ([]claudeRootSource, error) {
	primary := filepath.Clean(ClaudeConfigDir(home))
	key := home + "\x00" + primary
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && entry.unchanged() {
		return append([]claudeRootSource(nil), entry.sources...), nil
	}
	read := time.Now()
	files, filesKnown := stampPaths([]string{AgenthailConfigPath(home), daemonClaudeRootPath(home)}, os.Stat)
	_, candidates, err := c.resolve(home)
	if err != nil {
		return nil, err
	}
	links, linksKnown := stampPaths(candidates, os.Lstat)
	targets, targetsKnown := stampPaths(candidates, os.Stat)
	// Resolved again after the stamps, so whatever changes after them shows as a stamp mismatch.
	sources, _, err := c.resolve(home)
	if err != nil {
		return nil, err
	}
	if filesKnown && linksKnown && targetsKnown {
		c.mu.Lock()
		c.entries[key] = claudeRootsEntry{read: read, files: files, roots: append(links, targets...), sources: sources}
		c.mu.Unlock()
	}
	return append([]claudeRootSource(nil), sources...), nil
}

// A path that cannot be stated is not known missing, so it is never cached.
func stampPaths(paths []string, stat func(string) (os.FileInfo, error)) ([]pathStamp, bool) {
	stamps := make([]pathStamp, len(paths))
	for i, path := range paths {
		stamp, ok := stampPath(path, stat)
		if !ok {
			return nil, false
		}
		stamps[i] = stamp
	}
	return stamps, true
}

func stampPath(path string, stat func(string) (os.FileInfo, error)) (pathStamp, bool) {
	info, err := stat(path)
	if err != nil {
		return pathStamp{path: path, stat: stat}, os.IsNotExist(err)
	}
	return pathStamp{path: path, stat: stat, info: info}, true
}

// A root is checked only for the link and the folder it resolves to, which decide its identity.
func (e claudeRootsEntry) unchanged() bool {
	for _, file := range e.files {
		current, ok := stampPath(file.path, file.stat)
		if !ok || !samePath(file.info, current.info) {
			return false
		}
		if file.info != nil && (file.info.Size() != current.info.Size() || !UnchangedSince(e.read, file.info.ModTime(), current.info.ModTime())) {
			return false
		}
	}
	for _, root := range e.roots {
		current, ok := stampPath(root.path, root.stat)
		if !ok || !samePath(root.info, current.info) {
			return false
		}
	}
	return true
}

func samePath(before, after os.FileInfo) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.Mode() == after.Mode() && os.SameFile(before, after)
}
