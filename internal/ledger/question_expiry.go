package ledger

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type ExpiryOutcome string

const (
	ExpiryDefaulted ExpiryOutcome = "defaulted"
	ExpiryCut       ExpiryOutcome = "cut"
	ExpiryEscalated ExpiryOutcome = "escalated"
)

type Expiry struct {
	ProjectKey string        `json:"projectKey"`
	Question   Question      `json:"question"`
	Outcome    ExpiryOutcome `json:"outcome"`
	Effects    []string      `json:"effects,omitempty"`
}

func (s *Store) ProjectsWith(typ EventType) ([]string, error) {
	rows, err := s.read.Query(`SELECT DISTINCT project_key FROM ledger_events WHERE type=?`, string(typ))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) ExpireQuestions(projectKey string, now time.Time) ([]Expiry, error) {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var due []*Question
	for _, q := range board.questions {
		if q.Expired(now) && (q.AutoApplies() || !q.Escalated) {
			due = append(due, q)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].DeadlineMs < due[j].DeadlineMs })
	ids := &idIssuer{q: tx, projectKey: projectKey}
	var expiries []Expiry
	var events []Event
	add := func(typ EventType, payload any) error {
		data, err := json.Marshal(payload)
		if err == nil {
			events = append(events, Event{ProjectKey: projectKey, Type: typ, Payload: data, CreatedAt: now.UTC()})
		}
		return err
	}
	for _, q := range due {
		expiry := Expiry{ProjectKey: projectKey, Question: *q}
		switch {
		case q.AutoApplies():
			id, err := ids.next(EventDecision, "D")
			if err != nil {
				return nil, err
			}
			expiry.Outcome, expiry.Effects = ExpiryDefaulted, []string{id}
			text := fmt.Sprintf("%s: %s (default applied; no answer by the deadline)", q.Text, q.DefaultAnswer)
			if err := add(EventDecision, DecisionPayload{DecisionID: id, GoalID: q.GoalID, QuestionID: q.ID, Version: 1, Text: text, Principal: PrincipalDefault}); err != nil {
				return nil, err
			}
			if err := add(EventQuestionRes, QuestionResolvedPayload{QuestionID: q.ID, Answer: q.DefaultAnswer, Applied: true, Auto: true, Effects: expiry.Effects}); err != nil {
				return nil, err
			}
		case q.Kind != QuestionOutward:
			id, err := ids.next(EventCut, "X")
			if err != nil {
				return nil, err
			}
			expiry.Outcome, expiry.Effects = ExpiryCut, []string{id}
			cut := CutPayload{CutID: id, Target: q.Target, QuestionID: q.ID, AuthorSession: q.OwnerSessionID,
				Reason: fmt.Sprintf("default of %s on expiry: %s", q.ID, q.DefaultAnswer)}
			if board.clauses[q.Target] != nil {
				cut.ClauseID = q.Target
			}
			if err := add(EventCut, cut); err != nil {
				return nil, err
			}
			fallthrough
		default:
			if expiry.Outcome == "" {
				expiry.Outcome = ExpiryEscalated
			}
			if err := add(EventQuestionEsc, QuestionEscalatedPayload{QuestionID: q.ID, DeadlineMs: q.DeadlineMs}); err != nil {
				return nil, err
			}
		}
		expiries = append(expiries, expiry)
	}
	for _, e := range events {
		if _, err := insertEvent(tx, `INSERT`, e); err != nil {
			return nil, err
		}
	}
	return expiries, tx.Commit()
}
