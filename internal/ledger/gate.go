package ledger

import (
	"encoding/json"
	"sort"
	"time"
)

const (
	GateBlockCap       = 3
	PersonActiveWindow = 10 * time.Minute
)

type GateResult struct {
	ProjectKey string   `json:"projectKey"`
	Result     string   `json:"result,omitempty"`
	Blocked    []string `json:"blocked,omitempty"`
	Waived     []string `json:"waived,omitempty"`
	Open       []Clause `json:"open,omitempty"`
	Repeat     bool     `json:"repeat,omitempty"`
}

func (s *Store) GateTurn(projectKey, sessionID, turnID string, now time.Time) (GateResult, error) {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return GateResult{}, err
	}
	defer release()
	tx, err := s.db.Begin()
	if err != nil {
		return GateResult{}, err
	}
	defer tx.Rollback()
	result := GateResult{ProjectKey: projectKey}
	if prev, ok := board.gates.turns[gateKey(sessionID, turnID)]; ok && turnID != "" {
		result.Result, result.Blocked, result.Waived, result.Repeat = prev.Result, prev.Blocked, prev.Waived, true
		return result, nil
	}
	owned := board.ClausesOwnedBy(sessionID)
	sort.Slice(owned, func(i, j int) bool { return idNumber(owned[i].ID) < idNumber(owned[j].ID) })
	pending, err := attachment(tx, projectKey, board, sessionID)
	if err != nil {
		return GateResult{}, err
	}
	if len(owned) == 0 && len(pending.Unattached) == 0 {
		return result, nil
	}
	if board.personActive(sessionID, now) {
		result.Result = GateDeferredPerson
		return result, writeGate(tx, projectKey, GatePayload{Gate: GateG2, SessionID: sessionID, TurnID: turnID, Result: result.Result}, now)
	}
	progressed, err := progressSince(tx, projectKey, sessionID, board.gates.lastSeq[sessionID])
	if err != nil {
		return GateResult{}, err
	}
	var open []string
	for _, c := range owned {
		if !progressed[c.ID] && !c.liveWait(sessionID, now) && !board.gates.waived[gateKey(sessionID, c.ID)] {
			open = append(open, c.ID)
			result.Open = append(result.Open, c)
		}
	}
	for _, u := range pending.Unattached {
		if !board.gates.waived[gateKey(sessionID, u.ID)] {
			open = append(open, u.ID)
		}
	}
	for _, item := range open {
		if board.gates.blocks[gateKey(sessionID, item)] < GateBlockCap {
			result.Blocked = append(result.Blocked, item)
			continue
		}
		result.Waived = append(result.Waived, item)
		data, err := json.Marshal(WaiverPayload{Gate: GateG2, ClauseID: item, Reason: "re-block cap reached", SessionID: sessionID})
		if err != nil {
			return GateResult{}, err
		}
		if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: projectKey, Type: EventWaiver, Payload: data, ActorSessionID: sessionID, CreatedAt: now.UTC()}); err != nil {
			return GateResult{}, err
		}
	}
	switch {
	case len(result.Blocked) > 0:
		result.Result = GateBlocked
	case len(result.Waived) > 0:
		result.Result = GateWaived
	default:
		result.Result = GatePass
	}
	if err := writeGate(tx, projectKey, GatePayload{Gate: GateG2, SessionID: sessionID, TurnID: turnID, Result: result.Result, Blocked: result.Blocked, Waived: result.Waived}, now); err != nil {
		return GateResult{}, err
	}
	return result, nil
}

func writeGate(tx interface {
	execer
	Commit() error
}, projectKey string, payload GatePayload, now time.Time) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: projectKey, Type: EventGate, Payload: data, ActorSessionID: payload.SessionID, CreatedAt: now.UTC()}); err != nil {
		return err
	}
	return tx.Commit()
}

func (b *Board) personActive(sessionID string, now time.Time) bool {
	for _, u := range b.Utterances() {
		at, err := time.Parse(time.RFC3339, u.At)
		if err == nil && u.TargetID == sessionID && now.Sub(at) < PersonActiveWindow {
			return true
		}
	}
	return false
}

func (c Clause) liveWait(sessionID string, now time.Time) bool {
	for _, w := range c.Waits {
		if w.SessionID == sessionID && w.Resolved == "" && w.DeadlineMs > now.UnixMilli() {
			return true
		}
	}
	return false
}

func progressSince(q querier, projectKey, sessionID string, afterSeq int64) (map[string]bool, error) {
	rows, err := q.Query(`SELECT type,payload FROM ledger_events WHERE project_key=? AND actor_session_id=? AND seq>? AND type IN (?,?)`,
		projectKey, sessionID, afterSeq, string(EventClauseState), string(EventWait))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	progressed := map[string]bool{}
	for rows.Next() {
		var typ string
		var data []byte
		if err := rows.Scan(&typ, &data); err != nil {
			return nil, err
		}
		var p struct {
			ClauseID    string `json:"clauseId"`
			ProgressRef string `json:"progressRef"`
		}
		if json.Unmarshal(data, &p) != nil {
			continue
		}
		if EventType(typ) == EventWait || p.ProgressRef != "" {
			progressed[p.ClauseID] = true
		}
	}
	return progressed, rows.Err()
}
