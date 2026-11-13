//go:build !darwin

package filewake

import "github.com/fsnotify/fsnotify"

type fsnotifyFolder struct{ watcher *fsnotify.Watcher }

func newFolderWatcher() (folderWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return fsnotifyFolder{watcher: watcher}, nil
}

func (f fsnotifyFolder) Add(path string) error         { return f.watcher.Add(path) }
func (f fsnotifyFolder) Remove(path string) error      { return f.watcher.Remove(path) }
func (f fsnotifyFolder) Close() error                  { return f.watcher.Close() }
func (f fsnotifyFolder) Events() <-chan fsnotify.Event { return f.watcher.Events }
func (f fsnotifyFolder) Errors() <-chan error          { return f.watcher.Errors }
