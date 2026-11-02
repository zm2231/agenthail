package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type ledgerTarget struct {
	key     string
	localID string
}

// recordPersonWords records u in every project whose scope covers the session: its folder's project and any project it was opted into.
func (d *Daemon) recordPersonWords(session surface.Session, u ledger.Utterance) (string, bool, error) {
	if daemonOwned, err := d.Registry.StartedByDaemon(session.ID); err != nil || daemonOwned {
		return "", false, err
	}
	store := d.ledgerStore()
	targets, access, err := d.ledgerTargets(store, session)
	if err != nil {
		return "", false, err
	}
	recordedKey := ""
	for _, target := range targets {
		since, inScope, err := d.ledgerSince(store, target, session.ID, access)
		if err != nil {
			return target.key, false, err
		}
		if !inScope || u.At.Before(since) {
			continue
		}
		u.ProjectKey, u.SpeakerID, u.TargetID = target.key, surface.PersonSessionID, session.ID
		_, inserted, err := store.RecordUtterance(u)
		if err != nil {
			return target.key, false, err
		}
		if u.HookMissed(inserted) {
			d.publishEvent(ledger.EventCaptureMissed, session.ID, map[string]string{"projectKey": target.key, "ref": u.Ref, "sessionId": session.ID, "reason": "unknown"})
		}
		if recordedKey == "" {
			recordedKey = target.key
		}
	}
	return recordedKey, recordedKey != "", nil
}

// sessionProject uses the catalog's cached identity so per-prompt capture does not run git on the stream callback.
func (d *Daemon) sessionProject(session surface.Session) (*CatalogProject, error) {
	records, err := d.Registry.ListProjects()
	if err != nil {
		return nil, err
	}
	d.catalogIdentities.setProjects(records)
	identity := d.catalogIdentities.identity(context.Background(), session, time.Now(), func() *CatalogProject { return nil })
	if identity.Project == nil && identity.UnavailableReason != "" {
		return nil, fmt.Errorf("resolve the project of %s: %s", session.Cwd, identity.UnavailableReason)
	}
	return identity.Project, nil
}

func (d *Daemon) backfillPrompt(session surface.Session, payload sessionJournalPayload) {
	ref := journalPromptRef(payload)
	if ref == "" {
		return
	}
	if ledger.IsPromptRef(ref) && payload.MessageID != "" {
		if delivered := ledger.DeliveryRef(payload.MessageID); d.recordedFor(session, delivered) {
			ref = delivered
		}
	}
	at, err := time.Parse(time.RFC3339Nano, payload.TS)
	if err != nil {
		d.log.Printf("ledger backfill: prompt %s in %s has no time: %s", ref, session.ID, err)
		return
	}
	if _, _, err := d.recordPersonWords(session, ledger.Utterance{Ref: ref, Surface: string(session.Surface), Text: payload.Body, At: at, CapturedBy: ledger.CapturedByJournal}); err != nil {
		d.log.Printf("ledger backfill: record %s for %s: %s", ref, session.ID, err)
	}
}

func (d *Daemon) recordedFor(session surface.Session, ref string) bool {
	store := d.ledgerStore()
	targets, _, err := d.ledgerTargets(store, session)
	if err != nil {
		return false
	}
	for _, target := range targets {
		if found, err := store.HasRef(target.key, ref); err == nil && found {
			return true
		}
	}
	return false
}

func journalPromptRef(payload sessionJournalPayload) string {
	if payload.Kind != "message" || payload.Role != "user" || payload.Body == "" {
		return ""
	}
	if payload.Origin == surface.OriginPerson {
		if payload.MessageID == "" {
			return ""
		}
		return ledger.DeliveryRef(payload.MessageID)
	}
	if payload.Origin != "" || payload.PromptID == "" || !surfaces.IsTypedHumanPrompt(payload.Body) {
		return ""
	}
	return ledger.PromptRef(payload.PromptID)
}
