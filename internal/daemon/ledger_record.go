package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
)

type ledgerTarget struct {
	key       string
	projectID string
}

// personWordsRecord says where the person's words were recorded and whether the session is shown the provisional count there.
type personWordsRecord struct {
	key      string
	captured bool
	attach   bool
}

// recordPersonWords records u in every project whose scope covers the session: its folder's project and any project it was opted into.
// The record names the first project that shows the session its provisional count, else the first that recorded the words.
func (d *Daemon) recordPersonWords(session surface.Session, u ledger.Utterance) (personWordsRecord, error) {
	record, err := d.recordPersonWordsOnce(session, u)
	var refusal *ledger.Refusal
	if errors.As(err, &refusal) && refusal.MovedTo != "" {
		return d.recordPersonWordsOnce(session, u)
	}
	return record, err
}

func (d *Daemon) recordPersonWordsOnce(session surface.Session, u ledger.Utterance) (personWordsRecord, error) {
	if daemonOwned, err := d.Registry.StartedByDaemon(session.ID); err != nil || daemonOwned {
		return personWordsRecord{}, err
	}
	store := d.ledgerStore()
	targets, access, err := d.ledgerTargets(store, session)
	if err != nil {
		return personWordsRecord{}, err
	}
	var record personWordsRecord
	for _, target := range targets {
		scope, err := d.ledgerSince(store, target, session.ID, access)
		if err != nil {
			return personWordsRecord{key: target.key}, err
		}
		if !scope.inScope || u.At.Before(scope.since) {
			continue
		}
		u.ProjectKey, u.SpeakerID, u.TargetID = target.key, surface.PersonSessionID, session.ID
		recorded, inserted, err := store.RecordUtterance(u)
		if err != nil {
			return personWordsRecord{key: target.key}, err
		}
		if inserted {
			d.queueContext(contextJob{key: target.key, utteranceID: u.Ref, targetID: session.ID, seq: recorded.Seq, at: u.At})
		}
		if u.HookMissed(inserted) {
			d.publishEvent(ledger.EventCaptureMissed, session.ID, map[string]string{"projectKey": target.key, "ref": u.Ref, "sessionId": session.ID, "reason": "unknown"})
		}
		if !record.captured || (scope.attach && !record.attach) {
			record = personWordsRecord{key: target.key, captured: true, attach: scope.attach}
		}
	}
	return record, nil
}

// sessionProject is the current project of one session a request acts on,
// resolved from its own folder through the identity cache, which discovery
// keeps warm for every session it lists. Anything that covers many sessions
// uses recordedProject instead, which never runs git.
func (d *Daemon) sessionProject(session surface.Session) (*CatalogProject, error) {
	identity, err := d.sessionIdentity(session)
	if err != nil {
		return nil, err
	}
	if identity.Project == nil && identity.Problem != "" {
		return nil, fmt.Errorf("resolve the project of %s: %s", session.Cwd, identity.Problem)
	}
	return identity.Project, nil
}

// recordedProject is the project discovery last recorded for the session;
// found is false before discovery has recorded it.
func (d *Daemon) recordedProject(session surface.Session) (project *CatalogProject, found bool, err error) {
	raw, found, err := d.Registry.CatalogSessionProject(session.ID)
	if err != nil || !found {
		return nil, found, err
	}
	project, err = decodeRecordedProject(raw)
	if err != nil {
		return nil, true, fmt.Errorf("read the stored project of %s: %w", session.ID, err)
	}
	return project, true, nil
}

func decodeRecordedProject(raw []byte) (*CatalogProject, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var project *CatalogProject
	if err := json.Unmarshal(raw, &project); err != nil {
		return nil, err
	}
	if project == nil || project.ID == "" {
		return nil, nil
	}
	return project, nil
}

func (d *Daemon) sessionIdentity(session surface.Session) (catalogIdentity, error) {
	records, err := d.Registry.ListProjects()
	if err != nil {
		return catalogIdentity{}, err
	}
	d.catalogIdentities.setProjects(records)
	if err := d.refreshProjectLinks(); err != nil {
		return catalogIdentity{}, err
	}
	return d.catalogIdentities.identity(context.Background(), session, time.Now(), func() *CatalogProject { return nil }), nil
}

func (d *Daemon) backfillPrompt(session surface.Session, payload sessionJournalPayload) {
	if reason, ok := notPersonPrompt(payload); ok {
		d.notPerson.add(payload.PromptID, time.Now())
		d.reclassifyHookCapture(session, ledger.PromptRef(payload.PromptID), reason)
		return
	}
	ref := journalPromptRef(payload)
	if ref == "" {
		return
	}
	body := payload.Body
	if ledger.IsPromptRef(ref) && payload.MessageID != "" {
		if uuid, words, held := d.delivered.claim(session.ID, payload.PromptID, payload.MessageID, "", time.Now()); held {
			ref, body = ledger.DeliveryRef(uuid), words
		} else if delivered := ledger.DeliveryRef(payload.MessageID); d.recordedFor(session, delivered) {
			ref = delivered
		}
	}
	at, err := time.Parse(time.RFC3339Nano, payload.TS)
	if err != nil {
		d.log.Printf("ledger backfill: prompt %s in %s has no time: %s", ref, session.ID, err)
		return
	}
	if _, err := d.recordPersonWords(session, ledger.Utterance{Ref: ref, Surface: string(session.Surface), Text: body, At: at, CapturedBy: ledger.CapturedByJournal}); err != nil {
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
	if payload.Origin != "" || payload.PromptID == "" || !claudesurface.IsTypedHumanPrompt(payload.Body) {
		return ""
	}
	return ledger.PromptRef(payload.PromptID)
}
