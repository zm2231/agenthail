package daemon

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

// Without a named item the session's only item in progress is taken, as the PR watcher does.
func (d *Daemon) linkedWork(session surface.Session, named string) (heldWork, string, *mcpRefusal) {
	held, err := d.heldWork(session)
	if err != nil {
		return heldWork{}, "", refuse("internal_error", "read the work this session holds: "+err.Error())
	}
	if named != "" {
		for _, work := range held {
			if work.item.ID == named && (work.item.State == ledger.WorkInProgress || work.item.State == ledger.WorkBlocked) {
				return work, "", nil
			}
		}
		return heldWork{}, "", refuse("invalid", fmt.Sprintf("item %s is not held by this session in progress or blocked; name an item you hold, or leave item out", named))
	}
	var active []heldWork
	for _, work := range held {
		if work.item.State == ledger.WorkInProgress {
			active = append(active, work)
		}
	}
	switch len(active) {
	case 0:
		return heldWork{}, "", nil
	case 1:
		return active[0], "", nil
	}
	return heldWork{}, fmt.Sprintf("you hold %d items in progress; name one with item to show this on it", len(active)), nil
}
