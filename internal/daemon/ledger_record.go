package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func (d *Daemon) recordPersonWords(session surface.Session, u ledger.Utterance) (string, bool, error) {
	if session.Cwd == "" {
		return "", false, nil
	}
	project, err := d.sessionProject(session)
	if err != nil || project == nil || project.Key == "" {
		return "", false, err
	}
	store := ledger.NewStore(d.Registry.WriteDB(), d.Registry.ReadDB())
	since, inScope, err := d.ledgerSince(store, project, session.ID)
	if err != nil || !inScope || u.At.Before(since) {
		return project.Key, false, err
	}
	u.ProjectKey, u.SpeakerID, u.TargetID = project.Key, surface.PersonSessionID, session.ID
	if _, err := store.RecordUtterance(u); err != nil {
		return project.Key, false, err
	}
	return project.Key, true, nil
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
func (d *Daemon) ledgerSince(store *ledger.Store, project *CatalogProject, sessionID string) (time.Time, bool, error) {
	scope, err := store.Scope(project.Key)
	if err != nil || scope.State == ledger.LedgerDisabled {
		return time.Time{}, false, err
	}
	since, inScope := scope.Since, scope.State == ledger.LedgerEnabled
	if project.LocalID == "" {
		return since, inScope, nil
	}
	at, coordinator, err := d.Registry.CoordinatorSince(project.LocalID, sessionID)
	if err != nil {
		return time.Time{}, false, err
	}
	if coordinator && (!inScope || at.Before(since)) {
		since, inScope = at, true
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
	if _, _, err := d.recordPersonWords(session, ledger.Utterance{Ref: ref, Surface: string(session.Surface), Text: payload.Body, At: at, Backfilled: true}); err != nil {
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
