package registry

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

type HandoverRefusedError struct{ Reason string }

func (e HandoverRefusedError) Error() string { return e.Reason }

// Signing only in the step to done, which has no way back, keeps a host from owning what it signed away.
func signHandoverTx(tx *sql.Tx, move SessionMove, now time.Time) error {
	if move.HandoverSeq == 0 {
		return nil
	}
	self, err := hostIdentityTx(tx)
	if err != nil || move.PeerHost == self {
		return err
	}
	keyID, private, err := hostSignerTx(tx)
	if err != nil {
		return err
	}
	handover := hosts.Handover{Conversation: move.Conversation, From: self, To: move.PeerHost, MoveID: move.ID, Seq: move.HandoverSeq, KeyID: keyID, SignedAt: time.UnixMilli(now.UnixMilli())}
	handover.Signature = ed25519.Sign(private, handover.Statement())
	encoded, err := json.Marshal(handover)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE session_moves SET handover=? WHERE id=? AND role='out' AND state=?`, encoded, move.ID, MoveDone)
	return err
}

func (r *Registry) UndeliveredHandovers(peer string) ([]hosts.Handover, error) {
	return queryRows(r.read, []hosts.Handover{}, scanHandover, `SELECT handover FROM session_moves WHERE role='out' AND state=? AND peer_host=? AND handover<>'' AND handover_delivered_at_ms=0 ORDER BY updated_at, id`, MoveDone, peer)
}

func (r *Registry) MarkHandoverDelivered(moveID string, now time.Time) error {
	_, err := r.db.Exec(`UPDATE session_moves SET handover_delivered_at_ms=? WHERE id=? AND role='out'`, now.UnixMilli(), moveID)
	return err
}

func scanHandover(row rowScanner) (hosts.Handover, error) {
	var encoded []byte
	var handover hosts.Handover
	if err := row.Scan(&encoded); err != nil {
		return handover, err
	}
	return handover, json.Unmarshal(encoded, &handover)
}

// Resumed cannot be aborted, so an aborted move never serves a handover.
func (r *Registry) KeepHandover(handover hosts.Handover, source, self string) error {
	if handover.From != source || handover.To != self {
		return HandoverRefusedError{Reason: "the handover is not from the host that sent it to this host"}
	}
	move, err := r.SessionMove(handover.MoveID)
	if err != nil {
		return err
	}
	switch {
	case move.Role != "in" || move.PeerHost != source || move.Conversation != handover.Conversation:
		return HandoverRefusedError{Reason: fmt.Sprintf("move %s does not bring %s here from %s", move.ID, handover.Conversation, source)}
	case move.State != MoveResumed:
		return MoveConflictError{ID: move.ID, State: move.State, Want: MoveResumed}
	case move.HandoverSeq != handover.Seq:
		return HandoverRefusedError{Reason: fmt.Sprintf("move %s declared handover %d, not %d", move.ID, move.HandoverSeq, handover.Seq)}
	}
	var declared struct {
		Handovers []hosts.Handover `json:"handovers"`
	}
	_ = json.Unmarshal(move.Manifest, &declared)
	encoded, err := json.Marshal(hosts.TrimHandovers(append(declared.Handovers, handover)))
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`UPDATE session_moves SET handover=? WHERE id=? AND role='in' AND state=?`, encoded, move.ID, MoveResumed)
	return err
}

func (r *Registry) ServedHandovers(conversation string) ([]hosts.Handover, error) {
	var encoded []byte
	err := r.read.QueryRow(`SELECT m.handover FROM session_moves m JOIN sessions s ON s.id=m.conversation
		WHERE m.conversation=? AND m.role='in' AND m.state=? AND s.host_id='' AND m.handover<>''
		AND m.rowid=(SELECT o.rowid FROM session_moves o WHERE o.conversation=m.conversation AND o.role='in' AND o.state=? ORDER BY o.updated_at DESC, o.rowid DESC LIMIT 1)`, conversation, MoveResumed, MoveResumed).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var chain []hosts.Handover
	return chain, json.Unmarshal(encoded, &chain)
}

// The owner handed from is read in the same transaction, so the removal it publishes names the right host.
func (r *Registry) HandOverSigned(chain []hosts.Handover, conversation, servedBy string) (string, bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var from string
	var seq uint64
	err = tx.QueryRow(`SELECT host_id,handover_seq FROM sessions WHERE id=?`, conversation).Scan(&from, &seq)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (from == "" || from == servedBy) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	owner := from
	for _, handover := range chain {
		if owner == servedBy {
			break
		}
		if handover.From != owner || handover.Seq <= seq || handover.Conversation != conversation {
			continue
		}
		key, err := peerHostKeyTx(tx, handover.From, handover.KeyID)
		if errors.Is(err, sql.ErrNoRows) {
			return from, false, HandoverRefusedError{Reason: fmt.Sprintf("host %s published no key %s", handover.From, handover.KeyID)}
		}
		if err != nil {
			return from, false, err
		}
		if err := handover.Verify(key); err != nil {
			return from, false, HandoverRefusedError{Reason: err.Error()}
		}
		owner, seq = handover.To, handover.Seq
	}
	if owner != servedBy {
		return from, false, HandoverRefusedError{Reason: fmt.Sprintf("no newer signed handover of %s leads from host %s to the host serving it", conversation, from)}
	}
	handed, err := r.handOverTx(tx, from, conversation, hosts.Qualify(from, conversation), servedBy, seq)
	return from, handed, err
}
