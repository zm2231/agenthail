package filewake

import "github.com/fsnotify/fsnotify"

// folderWatcher watches one followed folder: the entries created in it, and
// the folder itself being removed or renamed. Close releases every descriptor
// it holds before it returns.
type folderWatcher interface {
	Add(path string) error
	Remove(path string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}
