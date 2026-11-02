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
	store := ledger.NewStore(d.Registry.WriteDB(), d.Registry.ReadDB())
	targets, grants, err := d.ledgerTargets(store, session)
	if err != nil {
		return "", false, err
	}
	recordedKey := ""
	for _, target := range targets {
		since, inScope, err := d.ledgerSince(store, target, session.ID, grants)
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

func (d *Daemon) ledgerTargets(store *ledger.Store, session surface.Session) ([]ledgerTarget, map[string]time.Time, error) {
	var targets []ledgerTarget
	if session.Cwd != "" {
		project, err := d.sessionProject(session)
		if err != nil {
			return nil, nil, err
		}
		if project != nil && project.Key != "" {
			targets = append(targets, ledgerTarget{key: project.Key, localID: project.LocalID})
		}
	}
	grants, err := store.SessionGrants(session.ID)
	if err != nil {
		return nil, nil, err
	}
	granted := map[string]time.Time{}
	for _, grant := range grants {
		granted[grant.ProjectKey] = grant.Since
		if len(targets) == 0 || targets[0].key != grant.ProjectKey {
			targets = append(targets, ledgerTarget{key: grant.ProjectKey})
		}
	}
	return targets, granted, nil
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

// ledgerSince is the earliest grant that puts sessionID in the project's ledger scope.
func (d *Daemon) ledgerSince(store *ledger.Store, target ledgerTarget, sessionID string, grants map[string]time.Time) (time.Time, bool, error) {
	scope, err := store.Scope(target.key)
	if err != nil || scope.State == ledger.LedgerDisabled {
		return time.Time{}, false, err
	}
	since, inScope := scope.Since, scope.State == ledger.LedgerEnabled
	consider := func(at time.Time) {
		if !inScope || at.Before(since) {
			since, inScope = at, true
		}
	}
	if at, ok := grants[target.key]; ok {
		consider(at)
	}
	if target.localID == "" {
		return since, inScope, nil
	}
	at, coordinator, err := d.Registry.CoordinatorSince(target.localID, sessionID)
	if err != nil {
		return time.Time{}, false, err
	}
	if coordinator {
		consider(at)
	}
	return since, inScope, nil
}

func (d *Daemon) backfillPrompt(session surface.Session, payload sessionJournalPayload) {
	ref := journalPromptRef(payload)
	if ref == "" {
		return
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
