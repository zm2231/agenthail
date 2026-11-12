package extension

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/zm2231/agenthail/internal/atomicfile"
)

// Approvals records, per extension, the capabilities the person granted. An
// extension holds only what it declares and was granted; declaring more
// later waits for a new approval.
type Approvals struct {
	path string
	mu   sync.Mutex
}

func NewApprovals(home string) *Approvals {
	return &Approvals{path: filepath.Join(DataDir(home), "approvals.json")}
}

func (a *Approvals) read() (map[string][]string, error) {
	data, err := os.ReadFile(a.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	granted := map[string][]string{}
	if err := json.Unmarshal(data, &granted); err != nil {
		return nil, err
	}
	return granted, nil
}

func (a *Approvals) Granted(id string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	granted, err := a.read()
	if err != nil {
		return nil, err
	}
	return granted[id], nil
}

func (a *Approvals) Grant(id string, capabilities []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	granted, err := a.read()
	if err != nil {
		return err
	}
	granted[id] = slices.Sorted(slices.Values(capabilities))
	data, err := json.MarshalIndent(granted, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(a.path, append(data, '\n'), 0o600)
}

// Effective splits what an extension declares into what it holds now and
// what still waits for the person.
func Effective(declared, granted []string) (held, pending []string) {
	for _, capability := range declared {
		if slices.Contains(granted, capability) {
			held = append(held, capability)
		} else {
			pending = append(pending, capability)
		}
	}
	return held, pending
}
