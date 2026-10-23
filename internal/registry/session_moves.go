package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// A move out is preparing until its request may have left this daemon, sent
// from then until the target resumed it (done) or provably never will
// (failed). A move in is prepared once the target accepted the manifest,
// received while the conversation's files arrive, and resumed or aborted.
const (
	MovePreparing = "preparing"
	MoveSent      = "sent"
	MoveDone      = "done"
	MoveFailed    = "failed"
	MovePrepared  = "prepared"
	MoveReceived  = "received"
	MoveResumed   = "resumed"
	MoveAborted   = "aborted"
)

type SessionMove struct {
	ID           string          `json:"id"`
	Conversation string          `json:"conversation"`
	PeerHost     string          `json:"peerHost"`
	Role         string          `json:"role"`
	State        string          `json:"state"`
	Manifest     json.RawMessage `json:"manifest,omitempty"`
	Detail       string          `json:"detail,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

var ErrMoveNotFound = errors.New("no such move")

// MoveConflictError refuses a move whose state does not allow the step.
type MoveConflictError struct {
	ID    string
	State string
	Want  string
}

func (e MoveConflictError) Error() string {
	return fmt.Sprintf("move %s is %s, not %s", e.ID, e.State, e.Want)
}

func scanSessionMove(row interface{ Scan(...any) error }) (SessionMove, error) {
	var move SessionMove
	var manifest []byte
	var created, updated string
	if err := row.Scan(&move.ID, &move.Conversation, &move.PeerHost, &move.Role, &move.State, &manifest, &move.Detail, &created, &updated); err != nil {
		return SessionMove{}, err
	}
	if len(manifest) > 0 {
		move.Manifest = manifest
	}
	move.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	move.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return move, nil
}

const sessionMoveColumns = `id,conversation,peer_host,role,state,manifest,detail,created_at,updated_at`

func (r *Registry) SessionMove(id string) (SessionMove, error) {
	move, err := scanSessionMove(r.read.QueryRow(`SELECT `+sessionMoveColumns+` FROM session_moves WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionMove{}, ErrMoveNotFound
	}
	return move, err
}

// SessionMoves lists moves, newest first; unfinished limits them to moves
// out still preparing or sent.
func (r *Registry) SessionMoves(unfinished bool) ([]SessionMove, error) {
	query := `SELECT ` + sessionMoveColumns + ` FROM session_moves`
	if unfinished {
		query += ` WHERE role='out' AND state IN ('preparing','sent')`
	}
	rows, err := r.read.Query(query + ` ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	moves := []SessionMove{}
	for rows.Next() {
		move, err := scanSessionMove(rows)
		if err != nil {
			return nil, err
		}
		moves = append(moves, move)
	}
	return moves, rows.Err()
}

// SessionMoveRefusal names what keeps a local session from moving: another
// host owning it, a move already holding it, a delivery in flight, an active
// relay, a channel or the coordinator role. It is empty when nothing does.
func (r *Registry) SessionMoveRefusal(conversation string) (string, error) {
	var owner string
	if err := r.read.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, conversation).Scan(&owner); err != nil {
		return "", err
	}
	if owner != "" {
		return "it runs on another host", nil
	}
	checks := []struct {
		query  string
		reason string
	}{
		{`SELECT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND role='out' AND state IN ('preparing','sent'))`, "a move of it has not finished"},
		{`SELECT EXISTS(SELECT 1 FROM message_queue WHERE session_id=? AND status='inflight')`, "a message to it is being delivered"},
		{`SELECT EXISTS(SELECT 1 FROM routes WHERE active=1 AND (from_session=?1 OR to_session=?1))`, "it is part of an active relay"},
		{`SELECT EXISTS(SELECT 1 FROM channel_members WHERE session_id=?)`, "it is in a channel"},
		{`SELECT EXISTS(SELECT 1 FROM coordinators c JOIN aliases a ON a.name=c.name WHERE c.host_id='' AND a.session_id=?)`, "it is a project coordinator"},
	}
	for _, check := range checks {
		var found bool
		if err := r.read.QueryRow(check.query, conversation).Scan(&found); err != nil {
			return "", err
		}
		if found {
			return check.reason, nil
		}
	}
	return "", nil
}

// BeginMoveOut records a move out as preparing. Only one move out of a
// conversation can be unfinished at a time.
func (r *Registry) BeginMoveOut(id, conversation, peerHost string, manifest []byte) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var busy bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND role='out' AND state IN ('preparing','sent'))`, conversation).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return fmt.Errorf("a move of %s has not finished", conversation)
	}
	stamp := r.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO session_moves(id,conversation,peer_host,role,state,manifest,created_at,updated_at) VALUES(?,?,?,'out',?,?,?,?)`, id, conversation, peerHost, MovePreparing, manifest, stamp, stamp); err != nil {
		return err
	}
	return tx.Commit()
}

// SetMoveState moves a move from one of from to state, recording detail.
func (r *Registry) SetMoveState(id, state, detail string, from ...string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setMoveStateTx(tx, r.now(), id, state, detail, from...); err != nil {
		return err
	}
	return tx.Commit()
}

func setMoveStateTx(tx *sql.Tx, now time.Time, id, state, detail string, from ...string) error {
	var current string
	err := tx.QueryRow(`SELECT state FROM session_moves WHERE id=?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMoveNotFound
	}
	if err != nil {
		return err
	}
	if current == state {
		return nil
	}
	allowed := len(from) == 0
	for _, candidate := range from {
		allowed = allowed || candidate == current
	}
	if !allowed {
		return MoveConflictError{ID: id, State: current, Want: fmt.Sprint(from)}
	}
	_, err = tx.Exec(`UPDATE session_moves SET state=?,detail=?,updated_at=? WHERE id=?`, state, detail, now.UTC().Format(time.RFC3339Nano), id)
	return err
}

// PrepareMoveIn records a move in as prepared with the manifest the source
// declared. Preparing it again with the same manifest is a no-op; a move
// already aborted, or prepared with another manifest, is refused.
func (r *Registry) PrepareMoveIn(id, conversation, sourceHost string, manifest []byte) (SessionMove, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return SessionMove{}, err
	}
	defer tx.Rollback()
	existing, err := scanSessionMove(tx.QueryRow(`SELECT `+sessionMoveColumns+` FROM session_moves WHERE id=?`, id))
	if err == nil {
		if existing.Role != "in" || existing.PeerHost != sourceHost || existing.Conversation != conversation || string(existing.Manifest) != string(manifest) {
			return SessionMove{}, fmt.Errorf("move %s was already prepared differently", id)
		}
		if existing.State == MoveAborted {
			return SessionMove{}, MoveConflictError{ID: id, State: existing.State, Want: MovePrepared}
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SessionMove{}, err
	}
	stamp := r.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`UPDATE session_moves SET state=?,detail='superseded by a later move from the same host',updated_at=? WHERE conversation=? AND role='in' AND peer_host=? AND state=? AND id<>?`, MoveAborted, stamp, conversation, sourceHost, MovePrepared, id); err != nil {
		return SessionMove{}, err
	}
	var open bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND id<>? AND ((role='out' AND state IN ('preparing','sent')) OR (role='in' AND state IN ('prepared','received'))))`, conversation, id).Scan(&open); err != nil {
		return SessionMove{}, err
	}
	if open {
		return SessionMove{}, fmt.Errorf("another move of %s has not finished here", conversation)
	}
	if _, err := tx.Exec(`INSERT INTO session_moves(id,conversation,peer_host,role,state,manifest,created_at,updated_at) VALUES(?,?,?,'in',?,?,?,?)`, id, conversation, sourceHost, MovePrepared, manifest, stamp, stamp); err != nil {
		return SessionMove{}, err
	}
	move, err := scanSessionMove(tx.QueryRow(`SELECT `+sessionMoveColumns+` FROM session_moves WHERE id=?`, id))
	if err != nil {
		return SessionMove{}, err
	}
	return move, tx.Commit()
}

// AbortMoveIn records that a move in will never be taken. A move not seen
// yet is recorded as aborted too, so a request that arrives late is refused.
// A move already resumed cannot be aborted.
func (r *Registry) AbortMoveIn(id, conversation, sourceHost string) (SessionMove, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return SessionMove{}, err
	}
	defer tx.Rollback()
	stamp := r.now().UTC().Format(time.RFC3339Nano)
	existing, err := scanSessionMove(tx.QueryRow(`SELECT `+sessionMoveColumns+` FROM session_moves WHERE id=?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO session_moves(id,conversation,peer_host,role,state,created_at,updated_at) VALUES(?,?,?,'in',?,?,?)`, id, conversation, sourceHost, MoveAborted, stamp, stamp); err != nil {
			return SessionMove{}, err
		}
	case err != nil:
		return SessionMove{}, err
	case existing.Role != "in" || existing.PeerHost != sourceHost:
		return SessionMove{}, fmt.Errorf("move %s is not a move from this host", id)
	case existing.State == MoveResumed:
		return existing, MoveConflictError{ID: id, State: existing.State, Want: MoveAborted}
	case existing.State == MoveAborted:
		return existing, nil
	default:
		if err := setMoveStateTx(tx, r.now(), id, MoveAborted, "aborted by the source", MovePrepared, MoveReceived); err != nil {
			return SessionMove{}, err
		}
	}
	move, err := scanSessionMove(tx.QueryRow(`SELECT `+sessionMoveColumns+` FROM session_moves WHERE id=?`, id))
	if err != nil {
		return SessionMove{}, err
	}
	return move, tx.Commit()
}

// TakeMovedSession makes an arrived conversation a local session of this
// host and records its move as resumed, in one transaction. The row the
// source host mirrored here is taken over; a row another host owns is taken
// only once that host no longer serves it.
// The source's alias is bound here when it is free.
func (r *Registry) TakeMovedSession(moveID string, session surface.Session, alias string, origin *SessionOrigin) ([]CatalogEvent, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	move, err := scanSessionMove(tx.QueryRow(`SELECT `+sessionMoveColumns+` FROM session_moves WHERE id=?`, moveID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMoveNotFound
	}
	if err != nil {
		return nil, err
	}
	if move.Role != "in" || move.Conversation != session.ID {
		return nil, fmt.Errorf("move %s does not bring %s here", moveID, session.ID)
	}
	if move.State != MoveReceived && move.State != MoveResumed {
		return nil, MoveConflictError{ID: moveID, State: move.State, Want: MoveReceived}
	}
	var events []CatalogEvent
	var owner string
	err = tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, session.ID).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil && owner == move.PeerHost {
		var fingerprint string
		if err := tx.QueryRow(`SELECT projection_fingerprint FROM catalog_sessions WHERE session_id=?`, session.ID).Scan(&fingerprint); err == nil {
			entity := projectionID(fingerprint, session.ID)
			payload, _ := json.Marshal(map[string]string{"sessionId": entity, "reason": "moved_here"})
			event, created, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + entity + ":move:" + moveID, Type: "session.removed", EntityID: entity, Payload: payload})
			if err != nil {
				return nil, err
			}
			if created {
				events = append(events, event)
			}
			if _, err := tx.Exec(`DELETE FROM catalog_sessions WHERE session_id=?`, session.ID); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE sessions SET host_id='',updated_at=datetime('now') WHERE id=?`, session.ID); err != nil {
			return nil, err
		}
	}
	if err := registerSessionTx(tx, session); err != nil {
		return nil, err
	}
	if alias != "" {
		var holder string
		err := tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, alias).Scan(&holder)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := setAliasTx(tx, alias, session.ID, r.now()); err != nil {
				return nil, err
			}
		case err != nil:
			return nil, err
		}
	}
	if origin != nil {
		if err := origin.valid(); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO session_origins(session_id,via,creator_session_id) VALUES(?,?,?) ON CONFLICT(session_id) DO UPDATE SET via=excluded.via,creator_session_id=excluded.creator_session_id`, session.ID, string(origin.Via), origin.CreatorID); err != nil {
			return nil, err
		}
	}
	if err := setMoveStateTx(tx, r.now(), moveID, MoveResumed, "", MoveReceived); err != nil {
		return nil, err
	}
	return events, tx.Commit()
}

// countUnheld counts the queued items a move fails rather than hands over.
func countUnheld(items []MovedQueueItem, held map[int64]HeldSend) int {
	count := 0
	for _, item := range items {
		if _, ok := held[item.ID]; !ok {
			count++
		}
	}
	return count
}

// HeldSend is a queued message a move hands to the target host, with the
// exact request the target will apply.
type HeldSend struct {
	QueueID int64
	Key     string
	Request []byte
}

// MovedQueueItem is a message queued for a conversation that is moving.
type MovedQueueItem struct {
	ID              int64
	Message         string
	Key             string
	SourceSessionID string
	BusyDelivery    string
	RelayHops       int
	Operation       QueueOperation
	Uploads         string
}

func (r *Registry) pendingMoveQueue(tx interface {
	Query(string, ...any) (*sql.Rows, error)
}, conversation string) ([]MovedQueueItem, error) {
	rows, err := tx.Query(`SELECT id,message,delivery_key,source_session_id,busy_delivery,relay_hops,operation,uploads FROM message_queue WHERE session_id=? AND host_request='' AND status='pending' ORDER BY id`, conversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MovedQueueItem{}
	for rows.Next() {
		var item MovedQueueItem
		if err := rows.Scan(&item.ID, &item.Message, &item.Key, &item.SourceSessionID, &item.BusyDelivery, &item.RelayHops, &item.Operation, &item.Uploads); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CompleteMoveOut hands a conversation the target resumed to the target, in
// one transaction: the target owns it from here, so every read and send of
// it routes there; its local alias is released; each queued message becomes
// a send held for the target; and the move is done. A queued operation the
// target cannot take as a send fails instead. The row is mirrored at once
// with placeholder, the projection shown until the target reports the
// session itself, so the target serves it from the moment it owns it.
func (r *Registry) CompleteMoveOut(moveID string, placeholder MovePlaceholder) ([]CatalogEvent, error) {
	for attempt := 0; attempt < 5; attempt++ {
		move, err := r.SessionMove(moveID)
		if err != nil {
			return nil, err
		}
		if move.State == MoveDone {
			return nil, nil
		}
		items, err := r.pendingMoveQueue(r.read, move.Conversation)
		if err != nil {
			return nil, err
		}
		held := map[int64]HeldSend{}
		for _, item := range items {
			if item.Operation != QueueOperationMessage || item.Uploads != "" && item.Uploads != "[]" {
				continue
			}
			key := item.Key
			if key == "" {
				key = fmt.Sprintf("move-%s-%d", moveID, item.ID)
			}
			request, err := r.hostSendRequest(HostSend{SessionID: move.Conversation, Message: item.Message, Key: key, SourceSessionID: item.SourceSessionID, BusyDelivery: item.BusyDelivery, RelayHops: item.RelayHops})
			if err != nil {
				return nil, err
			}
			held[item.ID] = HeldSend{QueueID: item.ID, Key: key, Request: request}
		}
		events, retry, err := r.completeMoveOutTx(move, items, held, placeholder)
		if err == nil && !retry && len(held) > 0 {
			r.signalChange(ChangeHostDeliveries)
		}
		if err != nil || !retry {
			return events, err
		}
	}
	return nil, fmt.Errorf("move %s: the queue kept changing while it was handed over", moveID)
}

// MovePlaceholder is the mirrored row a moved session shows until its new
// host reports it.
type MovePlaceholder struct {
	EntityID          string
	Projection        json.RawMessage
	UnavailableReason string
}

func (r *Registry) completeMoveOutTx(move SessionMove, items []MovedQueueItem, held map[int64]HeldSend, placeholder MovePlaceholder) ([]CatalogEvent, bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	current, err := r.pendingMoveQueue(tx, move.Conversation)
	if err != nil {
		return nil, false, err
	}
	if len(current) != len(items) {
		return nil, true, nil
	}
	for index := range current {
		if current[index].ID != items[index].ID {
			return nil, true, nil
		}
	}
	if err := setMoveStateTx(tx, r.now(), move.ID, MoveDone, "", MoveSent); err != nil {
		return nil, false, err
	}
	var events []CatalogEvent
	var observedAt string
	if err := tx.QueryRow(`SELECT observed_at FROM catalog_sessions WHERE session_id=?`, move.Conversation).Scan(&observedAt); err == nil {
		payload, _ := json.Marshal(map[string]string{"sessionId": move.Conversation, "reason": "moved_to_host"})
		event, created, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + move.Conversation + ":move:" + move.ID, Type: "session.removed", EntityID: move.Conversation, Payload: payload})
		if err != nil {
			return nil, false, err
		}
		if created {
			events = append(events, event)
		}
		if _, err := tx.Exec(`DELETE FROM catalog_sessions WHERE session_id=?`, move.Conversation); err != nil {
			return nil, false, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if _, err := tx.Exec(`UPDATE sessions SET host_id=?,pid=0,transcript='',has_local=0,bridge='',updated_at=datetime('now') WHERE id=? AND host_id=''`, move.PeerHost, move.Conversation); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(`DELETE FROM aliases WHERE session_id=?`, move.Conversation); err != nil {
		return nil, false, err
	}
	var projection map[string]any
	if err := json.Unmarshal(placeholder.Projection, &projection); err != nil || placeholder.EntityID == "" {
		return nil, false, fmt.Errorf("move %s: the moved row needs an entity and a projection", move.ID)
	}
	queued, err := queueCountTx(tx, move.Conversation)
	if err != nil {
		return nil, false, err
	}
	projection["queueCount"] = queued - countUnheld(items, held)
	encoded, err := json.Marshal(projection)
	if err != nil {
		return nil, false, err
	}
	movedAt := r.now().UTC()
	generation, err := nextProjectionGenerationTx(tx)
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,unavailable_reason,observed_at,projection_fingerprint,projection_generation,discovery_failures,host_queue_count,waiting_on,background_tasks)
		VALUES(?,?,?,?,?,?,?,0,0,?,?)`, move.Conversation, []byte(`null`), []byte(`{}`), placeholder.UnavailableReason, movedAt.Format(time.RFC3339Nano), string(encoded), generation, []byte{}, []byte{}); err != nil {
		return nil, false, err
	}
	event, created, err := appendHostUpsertTx(tx, placeholder.EntityID, projection, generation, movedAt, placeholder.UnavailableReason)
	if err != nil {
		return nil, false, err
	}
	if created {
		events = append(events, event)
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	now := r.now()
	for _, id := range ids {
		if send, ok := held[id]; ok {
			if _, err := tx.Exec(`UPDATE message_queue SET host_request=?,delivery_key=?,evidence=?,updated_at=datetime('now') WHERE id=?`, send.Request, send.Key, surface.EvidenceQueued, id); err != nil {
				return nil, false, err
			}
			continue
		}
		failure := "the session moved to another host before this was delivered; send it again there"
		if _, err := tx.Exec(`UPDATE message_queue SET status='dead',refused=0,dead_at_ms=?,last_error=?,available_at_ms=0,inflight_at_ms=0,updated_at=datetime('now') WHERE id=?`, now.UnixMilli(), failure, id); err != nil {
			return nil, false, err
		}
		if _, err := finishQueuedDeliveryIntent(tx, id, failure); err != nil {
			return nil, false, err
		}
	}
	return events, false, tx.Commit()
}
