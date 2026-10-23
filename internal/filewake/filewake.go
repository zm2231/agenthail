// Package filewake wakes readers that tail a file when it is written, or that
// list a directory when an entry appears in it, so a reader polling on a timer
// can react at once. A wake only says something may have changed; readers
// still re-read and keep their timer, which covers any change the platform
// does not report.
package filewake

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// DefaultLimit bounds how many files one process watches at once. On kqueue
// each watched file holds a descriptor; on inotify each holds a watch from the
// per-user budget. Past the limit a reader waits on its timer alone.
const DefaultLimit = 256

// Watcher shares one platform watcher across every reader in the process and
// keeps one watch per file however many readers follow it.
type Watcher struct {
	limit int
	logf  func(string, ...any)

	mu      sync.Mutex
	watcher *fsnotify.Watcher
	add     func(string) error
	broken  bool
	files   map[string]*watchedFile
	dirs    map[string]*watchedFile
	refused map[string]bool
}

type watchedFile struct {
	identity os.FileInfo
	armed    bool
	failed   bool
	wakes    map[*Wake]struct{}
}

// Wake is one reader's interest in a file or a directory. C fires after a
// reported change; it is nil when the path is not watched, which leaves the
// reader on its timer.
type Wake struct {
	owner *Watcher
	path  string
	dir   bool
	ch    chan struct{}
	once  sync.Once
}

var shared = New(DefaultLimit, log.Printf)

// Follow registers interest in path with the process-wide watcher.
func Follow(path string) *Wake { return shared.Follow(path) }

// FollowDir registers interest in new entries of dir with the process-wide
// watcher.
func FollowDir(dir string) *Wake { return shared.FollowDir(dir) }

// Watched reports whether the watcher is watching path.
func (w *Watcher) Watched(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	file := w.files[filepath.Clean(path)]
	return file != nil && file.armed
}

func New(limit int, logf func(string, ...any)) *Watcher {
	return &Watcher{limit: limit, logf: logf, files: map[string]*watchedFile{}, dirs: map[string]*watchedFile{}, refused: map[string]bool{}}
}

// Follow registers interest in path. Release the returned Wake when the reader
// stops.
func (w *Watcher) Follow(path string) *Wake { return w.follow(path, false) }

// FollowDir registers interest in dir: its Wake fires when an entry is created
// in it or moved into it, and not when an existing entry is written, so a
// reader that lists the directory learns of a new entry at once. Release the
// returned Wake when the reader stops.
func (w *Watcher) FollowDir(dir string) *Wake { return w.follow(dir, true) }

// watched is the map of followed files or of followed directories.
func (w *Watcher) watched(dir bool) map[string]*watchedFile {
	if dir {
		return w.dirs
	}
	return w.files
}

func (w *Watcher) follow(path string, dir bool) *Wake {
	path = filepath.Clean(path)
	wake := &Wake{owner: w, path: path, dir: dir, ch: make(chan struct{}, 1)}
	w.mu.Lock()
	defer w.mu.Unlock()
	watched := w.watched(dir)
	file := watched[path]
	if file == nil {
		if len(w.files)+len(w.dirs) >= w.limit || !w.startLocked() {
			wake.ch = nil
			return wake
		}
		file = &watchedFile{wakes: map[*Wake]struct{}{}, failed: w.refused[path]}
		if w.armLocked(path, file) != nil {
			// The platform refused the watch, as when its watch budget is
			// spent; the reader polls and the slot stays free. The refusal is
			// remembered so later readers of the path do not log it again;
			// past the watcher limit the memory starts over and may log again.
			if len(w.refused) >= w.limit {
				clear(w.refused)
			}
			w.refused[path] = true
			wake.ch = nil
			return wake
		}
		delete(w.refused, path)
		watched[path] = file
	}
	file.wakes[wake] = struct{}{}
	w.armLocked(path, file)
	return wake
}

// Close stops the watcher and releases its platform resources; readers that
// still follow a file fall back to their timers.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.broken = true
	w.files = map[string]*watchedFile{}
	w.dirs = map[string]*watchedFile{}
	if w.watcher == nil {
		return nil
	}
	err := w.watcher.Close()
	w.watcher = nil
	return err
}

// C is the channel that fires after the file changes.
func (k *Wake) C() <-chan struct{} { return k.ch }

// Refresh re-arms the watch when the path now names a different file, as after
// a rename over it, so later appends to the new file still wake the reader.
func (k *Wake) Refresh() {
	if k.ch == nil {
		return
	}
	w := k.owner
	w.mu.Lock()
	defer w.mu.Unlock()
	if file := w.watched(k.dir)[k.path]; file != nil {
		w.armLocked(k.path, file)
	}
}

// Release ends this reader's interest; the watch closes with its last reader.
func (k *Wake) Release() {
	k.once.Do(func() {
		if k.ch == nil {
			return
		}
		w := k.owner
		w.mu.Lock()
		defer w.mu.Unlock()
		watched := w.watched(k.dir)
		file := watched[k.path]
		if file == nil {
			return
		}
		delete(file.wakes, k)
		if len(file.wakes) > 0 {
			return
		}
		// A file and a directory are never one path, but a path followed both
		// ways keeps its platform watch until neither follows it.
		if file.armed && w.watched(!k.dir)[k.path] == nil {
			_ = w.watcher.Remove(k.path)
		}
		delete(watched, k.path)
	})
}

func (w *Watcher) startLocked() bool {
	if w.watcher != nil {
		return true
	}
	if w.broken {
		return false
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		w.broken = true
		w.logf("file watch unavailable, readers poll on their timer: %s", err)
		return false
	}
	w.watcher = watcher
	if w.add == nil {
		w.add = watcher.Add
	}
	go w.run(watcher)
	return true
}

// armLocked watches the file now at path, replacing a watch on a file that
// was renamed or removed from it. It returns the platform's refusal; a path
// with no file yet is armed by a later call once the file exists.
func (w *Watcher) armLocked(path string, file *watchedFile) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if file.armed && file.identity != nil && os.SameFile(file.identity, info) {
		return nil
	}
	if file.armed {
		_ = w.watcher.Remove(path)
		file.armed = false
	}
	if err := w.add(path); err != nil {
		if !file.failed {
			file.failed = true
			w.logf("watch %s, polling it on a timer: %s", path, err)
		}
		return err
	}
	file.armed, file.failed, file.identity = true, false, info
	return nil
}

func (w *Watcher) run(watcher *fsnotify.Watcher) {
	for {
		select {
		case event, open := <-watcher.Events:
			if !open {
				return
			}
			w.notify(filepath.Clean(event.Name), event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename), event.Has(fsnotify.Create))
		case err, open := <-watcher.Errors:
			if !open {
				return
			}
			w.logf("file watch: %s", err)
		}
	}
}

func watchingFileAt(path string, file *watchedFile) bool {
	info, err := os.Stat(path)
	return err == nil && file.identity != nil && os.SameFile(file.identity, info)
}

// notify wakes the readers of a changed file, and the readers of its
// directory when the change created it.
func (w *Watcher) notify(path string, gone, created bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if file := w.files[path]; file != nil {
		w.settleLocked(path, file, gone)
		wakeAll(file)
	}
	if dir := w.dirs[path]; dir != nil {
		w.settleLocked(path, dir, gone)
	}
	if dir := w.dirs[filepath.Dir(path)]; created && dir != nil {
		wakeAll(dir)
	}
}

// settleLocked drops the watch on a path whose file was removed or renamed
// away. A remove or rename can arrive after a reader already re-armed the
// path on the file that replaced the watched one; that watch stays.
func (w *Watcher) settleLocked(path string, file *watchedFile, gone bool) {
	if gone && file.armed && !watchingFileAt(path, file) {
		file.armed = false
		_ = w.watcher.Remove(path)
	}
}

func wakeAll(file *watchedFile) {
	for wake := range file.wakes {
		select {
		case wake.ch <- struct{}{}:
		default:
		}
	}
}
