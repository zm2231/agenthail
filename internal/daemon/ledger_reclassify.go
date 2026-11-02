package daemon

import (
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
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

func (d *Daemon) holdsUncheckedHookCapture(sessionID string) bool {
	held, err := d.ledgerStore().HoldsUncheckedHookCapture(sessionID)
	if err != nil {
		d.log.Printf("ledger reclassify: hook captures of %s: %s", sessionID, err)
		return true
	}
	return held
}

func (d *Daemon) reclassifyHookCapture(session surface.Session, ref, reason string) {
	store := d.ledgerStore()
	targets, _, err := d.ledgerTargets(store, session)
	if err != nil {
		d.log.Printf("ledger reclassify: projects of %s: %s", session.ID, err)
		return
	}
	for _, target := range targets {
		done, err := store.ReclassifyUtterance(target.key, ref, reason, time.Now())
		if err != nil {
			d.log.Printf("ledger reclassify: %s in %s: %s", ref, target.key, err)
			continue
		}
		if done {
			d.noticeRetractions(store, target.key, ref)
		}
	}
}

func (d *Daemon) noticeRetractions(store *ledger.Store, key, ref string) {
	board, err := store.Board(key)
	if err != nil {
		d.log.Printf("ledger reclassify: board of %s: %s", key, err)
		return
	}
	bySession := map[string][]string{}
	for _, r := range board.Retractions() {
		if r.UtteranceID != ref {
			continue
		}
		for _, session := range r.AttachedBy {
			bySession[session] = append(bySession[session], "  "+r.String())
		}
	}
	for session, lines := range bySession {
		text := "[intent ledger] " + ref + " in " + key + " was not the person's words, so what was built from it is taken back:\n" + strings.Join(lines, "\n") + "\nRedo any of it only from the person's own words."
		if _, err := d.Registry.QueueMessageWithKey(session, text, "ledger:retracted:"+ref+":"+session); err != nil {
			d.log.Printf("ledger reclassify: notify %s: %s", session, err)
		}
	}
}
