package voice

import (
	"slices"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/sessionstream"
)

const heldIDsMax = 2 * queuedRepliesMax

func heldIDs(receipt *delivery.Receipt, sessionID string) []string {
	var ids []string
	for _, id := range []string{receipt.TurnID, receipt.MessageID} {
		if id != "" && id != sessionID && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// The caller holds s.mu.
func (f *follower) hold(ids []string) {
	f.held = append(f.held, ids...)
	if len(f.held) > heldIDsMax {
		f.held = f.held[len(f.held)-heldIDsMax:]
	}
}

// The caller holds s.mu.
func (f *follower) releaseHeld(event sessionstream.Event) bool {
	i := slices.IndexFunc(f.held, func(id string) bool { return id == event.MessageID || id == event.TurnID })
	if i < 0 {
		return false
	}
	f.held = slices.Delete(f.held, i, i+1)
	return true
}
