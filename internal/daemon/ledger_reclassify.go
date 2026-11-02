package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// The transcript names the prompt behind a promptId; anything but the person's own prompt carries its origin or kind.
func notPersonPrompt(payload sessionJournalPayload) (string, bool) {
	if payload.PromptID == "" || payload.Origin == surface.OriginPerson {
		return "", false
	}
	if payload.Kind == "message" && payload.Role == "user" && payload.Origin == "" {
		return "", false
	}
	if payload.Origin != "" {
		return payload.Origin, true
	}
	return payload.Kind + ": " + payload.Title, true
}

func (d *Daemon) reclassifyHookCapture(session surface.Session, ref, reason string) {
	store := d.ledgerStore()
	targets, _, err := d.ledgerTargets(store, session)
	if err != nil {
		d.log.Printf("ledger reclassify: projects of %s: %s", session.ID, err)
		return
	}
	for _, target := range targets {
		if _, err := store.ReclassifyUtterance(target.key, ref, reason, time.Now()); err != nil {
			d.log.Printf("ledger reclassify: %s in %s: %s", ref, target.key, err)
		}
	}
}
