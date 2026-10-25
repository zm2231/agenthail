package claudepeer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
)

type OwnershipManifest struct {
	Agenthail     string `json:"agenthail"`
	State         string `json:"state"`
	Generation    string `json:"generation"`
	SourceID      string `json:"sourceSessionId"`
	ProcessToken  string `json:"processToken"`
	PID           int    `json:"pid"`
	ProcStart     string `json:"procStart"`
	ControlPath   string `json:"controlPath"`
	ControlDevice uint64 `json:"controlDevice"`
	ControlInode  uint64 `json:"controlInode"`
	SocketPath    string `json:"socketPath"`
	SocketDevice  uint64 `json:"socketDevice"`
	SocketInode   uint64 `json:"socketInode"`
	RecordPath    string `json:"recordPath"`
}

func RuntimeRoot(home string) string {
	return filepath.Join(home, ".agenthail", "run", "p")
}

func WorkerControlPath(home, generation string, pid int) string {
	return filepath.Join(RuntimeRoot(home), generation, strconv.Itoa(pid)+".sock")
}

func WorkerManifestPath(home, generation string, pid int) string {
	return filepath.Join(RuntimeRoot(home), generation, strconv.Itoa(pid)+".json")
}

func requireAbsent(paths ...string) error {
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("path already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func writeExclusiveJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create owned session record: %w", err)
	}
	encodeErr := json.NewEncoder(f).Encode(value)
	syncErr := f.Sync()
	closeErr := f.Close()
	if encodeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(encodeErr, syncErr, closeErr)
	}
	return nil
}

func writeExclusiveJSONAtomic(path string, value any) error {
	tmp := path + ".tmp." + uuid.NewString()
	if err := writeExclusiveJSON(tmp, value); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := os.Link(tmp, path); err != nil {
		return fmt.Errorf("publish owned record: %w", err)
	}
	return nil
}

func replaceManifest(path string, current, replacement OwnershipManifest) error {
	if !manifestOwned(path, current) {
		return fmt.Errorf("peer ownership manifest changed before activation")
	}
	tmp := path + ".tmp." + uuid.NewString()
	if err := writeExclusiveJSON(tmp, replacement); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if !manifestOwned(path, current) {
		return fmt.Errorf("peer ownership manifest changed before activation")
	}
	return os.Rename(tmp, path)
}

func manifestOwned(path string, expected OwnershipManifest) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var current OwnershipManifest
	return json.Unmarshal(data, &current) == nil && current == expected
}

func cleanupManifest(path string, manifest OwnershipManifest) {
	if manifestOwned(path, manifest) {
		_ = os.Remove(path)
	}
}
