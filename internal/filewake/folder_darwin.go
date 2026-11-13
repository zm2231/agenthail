package filewake

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
)

// kqueueFolder watches a folder through one kqueue watch on the folder alone,
// and finds a created entry by listing the folder when it changes. fsnotify's
// kqueue backend opens a descriptor for every entry of a watched folder, and
// was seen leaving one of them open after the folder's watcher closed.
type kqueueFolder struct {
	kq     int
	events chan fsnotify.Event
	errors chan error
	stop   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	path    string
	fd      int
	entries map[string]struct{}
	closed  bool
}

const folderNotes = unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME

func newFolderWatcher() (folderWatcher, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("kqueue: %w", err)
	}
	unix.CloseOnExec(kq)
	stop := []unix.Kevent_t{{Ident: 0, Filter: unix.EVFILT_USER, Flags: unix.EV_ADD | unix.EV_CLEAR}}
	if _, err := unix.Kevent(kq, stop, nil, nil); err != nil {
		_ = unix.Close(kq)
		return nil, fmt.Errorf("kqueue: %w", err)
	}
	f := &kqueueFolder{kq: kq, fd: -1, events: make(chan fsnotify.Event), errors: make(chan error), stop: make(chan struct{}), done: make(chan struct{})}
	go f.read()
	return f, nil
}

func (f *kqueueFolder) Add(path string) error {
	path = filepath.Clean(path)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fsnotify.ErrClosed
	}
	f.dropLocked()
	fd, err := unix.Open(path, unix.O_EVTONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "watch", Path: path, Err: err}
	}
	change := []unix.Kevent_t{{Ident: uint64(fd), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: folderNotes}}
	if _, err := unix.Kevent(f.kq, change, nil, nil); err != nil {
		_ = unix.Close(fd)
		return &os.PathError{Op: "watch", Path: path, Err: err}
	}
	f.path, f.fd, f.entries = path, fd, listEntries(path)
	return nil
}

func (f *kqueueFolder) Remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fd < 0 || filepath.Clean(path) != f.path {
		return fmt.Errorf("%w: %s", fsnotify.ErrNonExistentWatch, path)
	}
	f.dropLocked()
	return nil
}

// dropLocked closes the folder's descriptor, which also ends its kqueue watch.
func (f *kqueueFolder) dropLocked() {
	if f.fd >= 0 {
		_ = unix.Close(f.fd)
	}
	f.path, f.fd, f.entries = "", -1, nil
}

// Close stops the reader and waits for it, so the kqueue and the folder's
// descriptor are both closed when it returns.
func (f *kqueueFolder) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.dropLocked()
	f.mu.Unlock()
	close(f.stop)
	trigger := []unix.Kevent_t{{Ident: 0, Filter: unix.EVFILT_USER, Fflags: unix.NOTE_TRIGGER}}
	if _, err := unix.Kevent(f.kq, trigger, nil, nil); err != nil {
		return fmt.Errorf("stop kqueue: %w", err)
	}
	<-f.done
	return nil
}

func (f *kqueueFolder) Events() <-chan fsnotify.Event { return f.events }
func (f *kqueueFolder) Errors() <-chan error          { return f.errors }

func (f *kqueueFolder) read() {
	defer func() {
		_ = unix.Close(f.kq)
		close(f.events)
		close(f.errors)
		close(f.done)
	}()
	buffer := make([]unix.Kevent_t, 8)
	for {
		n, err := unix.Kevent(f.kq, nil, buffer, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			f.send(nil, fmt.Errorf("read kqueue: %w", err))
			return
		}
		for _, event := range buffer[:n] {
			if event.Filter == unix.EVFILT_USER {
				return
			}
			if !f.send(f.changes(int(event.Ident), event.Fflags), nil) {
				return
			}
		}
	}
}

// changes are the events a kqueue note on descriptor fd stands for: the
// folder itself removed or renamed, or each entry that appeared in it since
// it was last listed.
func (f *kqueueFolder) changes(fd int, notes uint32) []fsnotify.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fd != f.fd {
		return nil
	}
	switch {
	case notes&unix.NOTE_DELETE != 0:
		return []fsnotify.Event{{Name: f.path, Op: fsnotify.Remove}}
	case notes&unix.NOTE_RENAME != 0:
		return []fsnotify.Event{{Name: f.path, Op: fsnotify.Rename}}
	}
	listed := listEntries(f.path)
	var created []fsnotify.Event
	for name := range listed {
		if _, seen := f.entries[name]; !seen {
			created = append(created, fsnotify.Event{Name: filepath.Join(f.path, name), Op: fsnotify.Create})
		}
	}
	f.entries = listed
	return created
}

// send hands events and an error to the reader of the watcher; it reports
// false once the watcher is closing.
func (f *kqueueFolder) send(events []fsnotify.Event, err error) bool {
	for _, event := range events {
		select {
		case f.events <- event:
		case <-f.stop:
			return false
		}
	}
	if err != nil {
		select {
		case f.errors <- err:
		case <-f.stop:
			return false
		}
	}
	return true
}

func listEntries(path string) map[string]struct{} {
	entries, _ := os.ReadDir(path)
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = struct{}{}
	}
	return names
}
