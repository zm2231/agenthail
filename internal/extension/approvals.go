package extension

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/proxyacct"
)

// An extension holds only what it declares and was granted; declaring more,
// or pointing a mount at another upstream, waits for a new approval.
type Approvals struct {
	path string
	mu   sync.Mutex
}

type Grant struct {
	Capabilities []string              `json:"capabilities,omitempty"`
	Mounts       map[string]MountGrant `json:"mounts,omitempty"`
}

// Key names where the key is kept, never the key itself.
type MountGrant struct {
	Upstream string            `json:"upstream"`
	Key      *proxyacct.KeyRef `json:"key,omitempty"`
}

func NewApprovals(home string) *Approvals {
	return &Approvals{path: filepath.Join(DataDir(home), "approvals.json")}
}

func (a *Approvals) read() (map[string]Grant, error) {
	data, err := os.ReadFile(a.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Grant{}, nil
	}
	if err != nil {
		return nil, err
	}
	granted := map[string]Grant{}
	if err := json.Unmarshal(data, &granted); err != nil {
		return nil, err
	}
	return granted, nil
}

func (a *Approvals) write(granted map[string]Grant) error {
	data, err := json.MarshalIndent(granted, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(a.path, append(data, '\n'), 0o600)
}

func (a *Approvals) Granted(id string) (Grant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	granted, err := a.read()
	if err != nil {
		return Grant{}, err
	}
	return granted[id], nil
}

// A mount keeps its key only while its upstream stays the one the key was
// given for.
func (a *Approvals) Grant(id string, manifest Manifest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	granted, err := a.read()
	if err != nil {
		return err
	}
	previous := granted[id]
	next := Grant{Capabilities: slices.Sorted(slices.Values(manifest.Capabilities))}
	for _, mount := range manifest.Mounts {
		if next.Mounts == nil {
			next.Mounts = map[string]MountGrant{}
		}
		grant := MountGrant{Upstream: mount.Upstream}
		if kept, ok := previous.Mounts[mount.Name]; ok && kept.Upstream == mount.Upstream {
			grant.Key = kept.Key
		}
		next.Mounts[mount.Name] = grant
	}
	granted[id] = next
	return a.write(granted)
}

func (a *Approvals) SetKey(id string, mount Mount, key *proxyacct.KeyRef) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	granted, err := a.read()
	if err != nil {
		return err
	}
	grant := granted[id]
	current, ok := grant.Mounts[mount.Name]
	if !ok || current.Upstream != mount.Upstream {
		return fmt.Errorf("approve extension %s first: its mount %s is not approved for %s", id, mount.Name, mount.Upstream)
	}
	current.Key = key
	grant.Mounts[mount.Name] = current
	granted[id] = grant
	return a.write(granted)
}

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

func (g Grant) MountApproved(mount Mount) (MountGrant, bool) {
	grant, ok := g.Mounts[mount.Name]
	return grant, ok && grant.Upstream == mount.Upstream
}
