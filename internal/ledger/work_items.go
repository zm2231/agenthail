package ledger

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type WorkState string

const (
	WorkOpen       WorkState = "open"
	WorkInProgress WorkState = "in_progress"
	WorkBlocked    WorkState = "blocked"
	WorkDone       WorkState = "done"
)

type WorkAction string

const (
	WorkAdd     WorkAction = "add"
	WorkClaim   WorkAction = "claim"
	WorkBlock   WorkAction = "block"
	WorkUnblock WorkAction = "unblock"
	WorkClose   WorkAction = "done"
	WorkReopen  WorkAction = "reopen"
)

type WorkItem struct {
	ID        string    `json:"id"`
	ClauseID  string    `json:"clauseId"`
	Text      string    `json:"text"`
	State     WorkState `json:"state"`
	Assignee  string    `json:"assignee,omitempty"`
	CreatedBy string    `json:"createdBy"`
	Reason    string    `json:"reason,omitempty"`
}

type WorkRequest struct {
	ProjectKey string     `json:"projectKey"`
	SessionID  string     `json:"sessionId"`
	Action     WorkAction `json:"action"`
	ClauseID   string     `json:"clauseId,omitempty"`
	ItemID     string     `json:"itemId,omitempty"`
	Text       string     `json:"text,omitempty"`
	Reason     string     `json:"reason,omitempty"`
}

type WorkResult struct {
	Item     WorkItem `json:"item"`
	Readback string   `json:"readback"`
}

var workTransitions = map[WorkAction]struct {
	from []WorkState
	to   WorkState
}{
	WorkClaim:   {from: []WorkState{WorkOpen}, to: WorkInProgress},
	WorkBlock:   {from: []WorkState{WorkOpen, WorkInProgress}, to: WorkBlocked},
	WorkUnblock: {from: []WorkState{WorkBlocked}, to: WorkOpen},
	WorkClose:   {from: []WorkState{WorkInProgress}, to: WorkDone},
	WorkReopen:  {from: []WorkState{WorkDone}, to: WorkOpen},
}

func (s *Store) Work(req WorkRequest, now time.Time) (WorkResult, error) {
	if req.ProjectKey == "" || req.SessionID == "" {
		return WorkResult{}, refuse("project and acting session are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return WorkResult{}, err
	}
	defer tx.Rollback()
	board, err := loadBoard(tx, req.ProjectKey)
	if err != nil {
		return WorkResult{}, err
	}
	typ, payload, readback, err := board.planWork(req, &idIssuer{q: tx, projectKey: req.ProjectKey})
	if err != nil {
		return WorkResult{}, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return WorkResult{}, err
	}
	event, err := insertEvent(tx, `INSERT`, Event{ProjectKey: req.ProjectKey, Type: typ, Payload: data, ActorSessionID: req.SessionID, CreatedAt: now.UTC()})
	if err != nil {
		return WorkResult{}, err
	}
	if err := board.Apply(event); err != nil {
		return WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkResult{}, err
	}
	id := req.ItemID
	if p, ok := payload.(WorkItemPayload); ok {
		id = p.ItemID
	}
	return WorkResult{Item: *board.workItems[id], Readback: "intent ledger: " + readback}, nil
}

func (b *Board) planWork(req WorkRequest, ids *idIssuer) (EventType, any, string, error) {
	if req.Action == WorkAdd {
		c := b.clauses[req.ClauseID]
		if c == nil {
			return "", nil, "", refuse("a work item hangs off a clause; %q is not a clause in %s", req.ClauseID, req.ProjectKey)
		}
		if c.State == StateRejected || clauseFullyCut(c) {
			return "", nil, "", refuse("%s is no longer in scope; work items hang off live clauses", c.ID)
		}
		text := strings.TrimSpace(req.Text)
		if text == "" {
			return "", nil, "", refuse("a work item needs its text")
		}
		id, err := ids.next(EventWorkItem, "W")
		if err != nil {
			return "", nil, "", err
		}
		return EventWorkItem, WorkItemPayload{ItemID: id, ClauseID: c.ID, Text: text, CreatedBy: req.SessionID}, fmt.Sprintf("+%s on %s", id, c.ID), nil
	}
	transition, ok := workTransitions[req.Action]
	if !ok {
		return "", nil, "", refuse("unknown work action %q; use add, claim, block, unblock, done or reopen", req.Action)
	}
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	allowed := false
	for _, from := range transition.from {
		allowed = allowed || item.State == from
	}
	if !allowed {
		return "", nil, "", refuse("%s is %s; %s needs it %s", item.ID, item.State, req.Action, joinStates(transition.from))
	}
	reason := strings.TrimSpace(req.Reason)
	assignee := item.Assignee
	switch req.Action {
	case WorkClaim:
		assignee = req.SessionID
	case WorkClose:
		if item.Assignee != req.SessionID {
			return "", nil, "", refuse("%s is claimed by %s; only its assignee closes it", item.ID, item.Assignee)
		}
	case WorkBlock:
		if reason == "" {
			return "", nil, "", refuse("block needs --reason: what %s waits on", item.ID)
		}
	}
	payload := WorkItemStatePayload{ItemID: item.ID, FromState: item.State, ToState: transition.to, Actor: req.SessionID, Assignee: assignee, Reason: reason}
	return EventWorkItemState, payload, fmt.Sprintf("%s %s (%s)", item.ID, transition.to, item.ClauseID), nil
}

func joinStates(states []WorkState) string {
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, " or ")
}

func (b *Board) applyWorkItem(data []byte) error {
	var p WorkItemPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	item := &WorkItem{ID: p.ItemID, ClauseID: p.ClauseID, Text: p.Text, State: WorkOpen, CreatedBy: p.CreatedBy}
	b.workItems[p.ItemID] = item
	if c := b.clauses[p.ClauseID]; c != nil {
		c.WorkItems = append(c.WorkItems, item)
	}
	return nil
}

func (b *Board) applyWorkItemState(data []byte) error {
	var p WorkItemStatePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if item := b.workItems[p.ItemID]; item != nil {
		item.State, item.Assignee, item.Reason = p.ToState, p.Assignee, p.Reason
	}
	return nil
}

func (b *Board) WorkItems() []WorkItem {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]WorkItem, 0, len(b.workItems))
	for _, item := range b.workItems {
		out = append(out, *item)
	}
	return out
}
