package registry

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type Wait struct {
	Ref        string
	Kind       surface.WaitingKind
	Since      time.Time
	Answerable bool
}

type WaitSync struct {
	Opened []Ask
	Closed []Ask
}

func (s WaitSync) Changed() bool { return len(s.Opened)+len(s.Closed) > 0 }

var waitTitles = map[surface.WaitingKind]string{
	surface.WaitingQuestion: "Asks you a question",
	surface.WaitingInput:    "Needs your input",
	surface.WaitingDialog:   "Has a dialog open in its terminal",
}

func waitTitle(kind surface.WaitingKind) string {
	if title := waitTitles[kind]; title != "" {
		return title
	}
	return "Needs your approval"
}

func (r *Registry) openPermissionAsks(ctx context.Context, q askQuerier, sessionID string) ([]Ask, error) {
	return queryAsks(ctx, q, `WHERE session_id=? AND origin=? AND state=? ORDER BY created_at_ms, id`, sessionID, AskOriginPermission, AskOpen)
}

// NeedsRoute reports whether waits holds an answerable wait that has no ask yet, the only case SyncWaits routes.
func (r *Registry) NeedsRoute(sessionID string, waits []Wait) (bool, error) {
	for _, wait := range waits {
		if !wait.Answerable {
			continue
		}
		var known bool
		if err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM asks WHERE session_id=? AND origin=? AND ref=?)`, sessionID, AskOriginPermission, wait.Ref).Scan(&known); err != nil {
			return false, err
		}
		if !known {
			return true, nil
		}
	}
	return false, nil
}

// SyncWaits makes a session's open permission asks the waits its provider holds open now: one ask per new wait, routed to routedTo when answerable and to the person when not, and every open one whose wait closed withdrawn.
func (r *Registry) SyncWaits(sessionID string, waits []Wait, routedTo string, now time.Time) (WaitSync, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return WaitSync{}, err
	}
	defer tx.Rollback()
	open, err := queryAsks(context.Background(), tx, `WHERE session_id=? AND origin=? AND state=? ORDER BY created_at_ms, id`, sessionID, AskOriginPermission, AskOpen)
	if err != nil {
		return WaitSync{}, err
	}
	var sync WaitSync
	wanted := make(map[string]bool, len(waits))
	for _, wait := range waits {
		wanted[wait.Ref] = true
	}
	at := time.UnixMilli(now.UnixMilli()).UTC()
	for _, ask := range open {
		if wanted[ask.Ref] {
			continue
		}
		if _, err := tx.Exec(`UPDATE asks SET state=?,answered_at_ms=? WHERE id=? AND state=?`, AskWithdrawn, at.UnixMilli(), ask.ID, AskOpen); err != nil {
			return WaitSync{}, err
		}
		sync.Closed = append(sync.Closed, ask)
	}
	for _, wait := range waits {
		to := routedTo
		if !wait.Answerable || to == "" {
			to = AskToPerson
		}
		since := at
		if !wait.Since.IsZero() {
			since = time.UnixMilli(wait.Since.UnixMilli()).UTC()
		}
		id, err := newAskID()
		if err != nil {
			return WaitSync{}, err
		}
		route, err := json.Marshal([]AskHop{{To: to, At: at, Why: AskRaised}})
		if err != nil {
			return WaitSync{}, err
		}
		result, err := tx.Exec(`INSERT OR IGNORE INTO asks(id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,created_at_ms,ref,wait_kind,answerable) VALUES(?,?,?,?,'[]',?,?,?,?,?,?,?,?)`,
			id, sessionID, AskOriginPermission, waitTitle(wait.Kind), to, at.UnixMilli(), string(route), AskOpen, since.UnixMilli(), wait.Ref, string(wait.Kind), wait.Answerable)
		if err != nil {
			return WaitSync{}, err
		}
		if inserted, err := result.RowsAffected(); err != nil {
			return WaitSync{}, err
		} else if inserted == 0 {
			continue
		}
		sync.Opened = append(sync.Opened, Ask{ID: id, SessionID: sessionID, Origin: AskOriginPermission, Title: waitTitle(wait.Kind), Questions: []AskQuestion{}, RoutedTo: to, RoutedAt: at,
			Route: []AskHop{{To: to, At: at, Why: AskRaised}}, State: AskOpen, CreatedAt: since, Ref: wait.Ref, WaitKind: wait.Kind, Answerable: wait.Answerable})
	}
	return sync, tx.Commit()
}

// SessionWaiting is what a session waits on, from its open permission asks: the oldest, with every open answerable one when it is answerable.
func (r *Registry) SessionWaiting(sessionID string) (*surface.Waiting, error) {
	open, err := r.openPermissionAsks(context.Background(), r.read, sessionID)
	if err != nil || len(open) == 0 {
		return nil, err
	}
	return waitingOf(open), nil
}

func (r *Registry) OpenPermissionAsks(sessionID string) ([]Ask, error) {
	return r.openPermissionAsks(context.Background(), r.read, sessionID)
}

func waitingOf(open []Ask) *surface.Waiting {
	oldest := open[0]
	waiting := &surface.Waiting{Kind: oldest.WaitKind, ID: oldest.ID, Since: oldest.CreatedAt, Answerable: oldest.Answerable}
	if oldest.Answerable {
		for _, ask := range open {
			if ask.Answerable {
				waiting.Open = append(waiting.Open, ask.ID)
			}
		}
	}
	return waiting
}

func (r *Registry) MarkAskAnnounced(id string, at time.Time) error {
	_, err := r.db.Exec(`UPDATE asks SET announced_at_ms=? WHERE id=? AND announced_at_ms=0`, at.UnixMilli(), id)
	return err
}
