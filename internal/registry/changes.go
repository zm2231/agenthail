package registry

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ChangeReason names a kind of committed write another process may want to
// react to at once rather than on its next timer.
type ChangeReason string

// ChangeHostDeliveries is a send held for a paired host.
const ChangeHostDeliveries ChangeReason = "host-deliveries"

// ChangeSignal returns the file that writes for reason touch after they
// commit, creating it so a watcher can follow it before the first write. A
// touch is only a hint: a follower still keeps its timer for any it misses.
func (r *Registry) ChangeSignal(reason ChangeReason) (string, error) {
	path := r.changeSignalPath(reason)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	return path, file.Close()
}

func (r *Registry) changeSignalPath(reason ChangeReason) string {
	return filepath.Join(filepath.Dir(r.path), "changes", string(reason))
}

// signalChange touches reason's signal file. It never fails the write that
// called it.
func (r *Registry) signalChange(reason ChangeReason) {
	path := r.changeSignalPath(reason)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = os.WriteFile(path, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0o600)
}
