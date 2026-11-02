package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

const (
	RetractedAsk        = "ask"
	RetractedClause     = "clause"
	RetractedAdoption   = "adoption"
	RetractedDecision   = "decision"
	RetractedAnswer     = "answer"
	RetractedReview     = "review"
	RetractedCutAck     = "cut-ack"
	RetractedCutCover   = "cut-withdrawal"
	RetractedDelegation = "delegation"
	RetractedRevision   = "revision"
	RetractedClauseMove = "clause-state"
	RetractedCut        = "cut"
)

type Retraction struct {
	Kind        string   `json:"kind"`
	ID          string   `json:"id"`
	UtteranceID string   `json:"utteranceId"`
	Reason      string   `json:"reason"`
	AttachedBy  []string `json:"attachedBy,omitempty"`
	ReSourcedTo string   `json:"reSourcedTo,omitempty"`
	// Outward marks an answer or approval that may already have let a session merge, deploy or send.
	Outward bool `json:"authorizedOutward,omitempty"`
}

func (r Retraction) String() string {
	line := fmt.Sprintf("retracted: %s %s built from %s words, not the person's (%s)", r.Kind, r.ID, r.Reason, r.UtteranceID)
	if r.ReSourcedTo != "" {
		line += fmt.Sprintf("; kept only because the person built on it: re-sourced to %s", r.ReSourcedTo)
	}
	if r.Outward {
		line += "; it authorized an outward act: check whether a merge, deploy or send was done on it"
	}
	return line
}

func (b *Board) Retractions() []Retraction {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := slices.Clone(b.retractions)
	for i := range out {
		out[i].AttachedBy = slices.Clone(b.attachedBy[out[i].UtteranceID])
	}
	return out
}

// errNeedsReplay asks the store to replay the project: live words build on something dropped as retracted.
var errNeedsReplay = errors.New("ledger: the board needs a replay")

type eventSource struct {
	UtteranceID       string `json:"utteranceId"`
	SourceUtteranceID string `json:"sourceUtteranceId"`
	FromUtterance     string `json:"fromUtterance"`
	EvidenceRef       string `json:"evidenceRef"`
	AskID             string `json:"askId"`
	ClauseID          string `json:"clauseId"`
	DecisionID        string `json:"decisionId"`
	CutID             string `json:"cutId"`
	QuestionID        string `json:"questionId"`
	ItemID            string `json:"itemId"`
	SessionID         string `json:"sessionId"`
}

func (s eventSource) ref() string {
	for _, ref := range []string{s.UtteranceID, s.SourceUtteranceID, s.FromUtterance, s.EvidenceRef} {
		if ref != "" {
			return ref
		}
	}
	return ""
}

func (b *Board) collectRetracted(events []Event) error {
	for _, e := range events {
		if e.Type != EventReclassified {
			continue
		}
		var p ReclassifiedPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return err
		}
		b.retracted[p.UtteranceID] = p.Reason
	}
	if len(b.retracted) == 0 {
		return nil
	}
	askOf := map[string]string{}
	for _, e := range events {
		var src eventSource
		if json.Unmarshal(e.Payload, &src) != nil {
			continue
		}
		if e.Type == EventClause {
			askOf[src.ClauseID] = src.AskID
		}
		ref := src.ref()
		if _, retracted := b.retracted[ref]; ref == "" || retracted {
			continue
		}
		for _, key := range builtOn(e.Type, src, askOf) {
			if _, ok := b.kept[key]; !ok {
				b.kept[key] = ref
			}
		}
	}
	return nil
}

// builtOn names the asks, clauses and decisions an event extends; one built from reclassified words stays, re-sourced to the person's.
func builtOn(t EventType, src eventSource, askOf map[string]string) []string {
	switch t {
	case EventClause, EventDelegation:
		return []string{skippedKey(RetractedAsk, src.AskID)}
	case EventSupersession, EventClauseSource:
		return []string{skippedKey(RetractedClause, src.ClauseID), skippedKey(RetractedAsk, askOf[src.ClauseID])}
	case EventDecisionVer:
		return []string{skippedKey(RetractedDecision, src.DecisionID)}
	}
	return nil
}

func createdKey(t EventType, src eventSource) string {
	switch t {
	case EventAsk:
		return skippedKey(RetractedAsk, src.AskID)
	case EventClause:
		return skippedKey(RetractedClause, src.ClauseID)
	case EventDecision:
		return skippedKey(RetractedDecision, src.DecisionID)
	}
	return ""
}

// screen skips every event sourced from reclassified words, as if they had never been captured.
func (b *Board) screen(event Event) (skip bool, resource func(), err error) {
	if len(b.retracted) == 0 || event.Type == EventReclassified {
		return false, nil, nil
	}
	var src eventSource
	if json.Unmarshal(event.Payload, &src) != nil {
		return false, nil, nil
	}
	ref := src.ref()
	if ref == "" {
		return false, nil, nil
	}
	reason, retracted := b.retracted[ref]
	if !retracted {
		for _, key := range builtOn(event.Type, src, nil) {
			if b.dropped[key] {
				return false, nil, fmt.Errorf("%w: %s builds on %s", errNeedsReplay, ref, key)
			}
		}
		return false, nil, nil
	}
	r := b.retraction(event, src, ref, reason)
	key := createdKey(event.Type, src)
	if key != "" && b.kept[key] != "" {
		r.ReSourcedTo = b.kept[key]
		b.retractions = append(b.retractions, r)
		return false, func() { b.resource(event.Type, src, r.ReSourcedTo) }, nil
	}
	if key != "" {
		b.dropped[key] = true
	}
	if r.Kind != "" {
		b.retractions = append(b.retractions, r)
	}
	return true, nil, nil
}

func (b *Board) resource(t EventType, src eventSource, ref string) {
	switch t {
	case EventAsk:
		if ask := b.asks[src.AskID]; ask != nil {
			ask.SourceUtterance = ref
		}
	case EventClause:
		if c := b.clauses[src.ClauseID]; c != nil {
			c.Sources = nil
		}
	case EventDecision:
		if d := b.decisions[src.DecisionID]; d != nil {
			d.SourceUtterance = ref
		}
	}
}

func (b *Board) retraction(event Event, src eventSource, ref, reason string) Retraction {
	r := Retraction{UtteranceID: ref, Reason: reason}
	switch event.Type {
	case EventUtterance:
		return Retraction{}
	case EventAttach:
		if !slices.Contains(b.attachedBy[ref], src.SessionID) {
			b.attachedBy[ref] = append(b.attachedBy[ref], src.SessionID)
		}
		return Retraction{}
	case EventAsk:
		r.Kind, r.ID = RetractedAsk, src.AskID
	case EventClause:
		r.Kind, r.ID = RetractedClause, src.ClauseID
	case EventDecision:
		r.Kind, r.ID, r.Outward = RetractedDecision, src.DecisionID, b.outwardQuestion(src.QuestionID)
	case EventDecisionVer:
		r.Kind, r.ID, r.Outward = RetractedDecision, src.DecisionID, b.outwardQuestion(src.QuestionID)
		if d := b.decisions[src.DecisionID]; d != nil {
			r.Outward = r.Outward || b.outwardQuestion(d.QuestionID)
		}
	case EventQuestionRes:
		r.Kind, r.ID, r.Outward = RetractedAnswer, src.QuestionID, b.outwardQuestion(src.QuestionID)
	case EventWorkItemState:
		r.Kind, r.ID, r.Outward = RetractedReview, src.ItemID, true
	case EventCut:
		r.Kind, r.ID = RetractedCut, src.CutID
	case EventCutAck:
		r.Kind, r.ID = RetractedCutAck, src.CutID
	case EventCutWithdrawn:
		r.Kind, r.ID = RetractedCutCover, src.CutID
	case EventDelegation:
		r.Kind, r.ID = RetractedDelegation, src.AskID
	case EventSupersession:
		r.Kind, r.ID = RetractedRevision, src.ClauseID
	case EventClauseSource:
		r.Kind, r.ID = RetractedAdoption, src.ClauseID
	case EventClauseState:
		r.Kind, r.ID = RetractedClauseMove, src.ClauseID
	default:
		r.Kind = string(event.Type)
	}
	return r
}

func (b *Board) outwardQuestion(id string) bool {
	q := b.questions[id]
	return q != nil && q.Kind == QuestionOutward
}

func skippedKey(kind, id string) string { return kind + ":" + id }
