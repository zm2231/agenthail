package ledger

import (
	"encoding/json"
	"strings"
	"time"
)

type WorkState string

const (
	WorkOpen       WorkState = "open"
	WorkInProgress WorkState = "in_progress"
	WorkBlocked    WorkState = "blocked"
	WorkInReview   WorkState = "in_review"
	WorkDone       WorkState = "done"
)

type WorkAction string

const (
	WorkAdd     WorkAction = "add"
	WorkClaim   WorkAction = "claim"
	WorkBlock   WorkAction = "block"
	WorkUnblock WorkAction = "unblock"
	WorkSubmit  WorkAction = "submit"
	WorkApprove WorkAction = "approve"
	WorkReject  WorkAction = "reject"
	WorkReopen  WorkAction = "reopen"
	WorkHandoff WorkAction = "handoff"
	WorkDep     WorkAction = "dep"
)

type Handoff struct {
	Done      []string `json:"done,omitempty"`
	Remaining []string `json:"remaining,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Uncertain []string `json:"uncertain,omitempty"`
	By        string   `json:"by,omitempty"`
}

func (h Handoff) empty() bool {
	return len(h.Done)+len(h.Remaining)+len(h.Decisions)+len(h.Uncertain) == 0
}

type WorkItem struct {
	ID        string    `json:"id"`
	ClauseID  string    `json:"clauseId"`
	Text      string    `json:"text"`
	State     WorkState `json:"state"`
	Assignee  string    `json:"assignee,omitempty"`
	CreatedBy string    `json:"createdBy"`
	Reason    string    `json:"reason,omitempty"`
	Reviewer  string    `json:"reviewer,omitempty"`
	DependsOn []string  `json:"dependsOn,omitempty"`
	Handoff   *Handoff  `json:"handoff,omitempty"`
}

type WorkRequest struct {
	ProjectKey  string     `json:"projectKey"`
	SessionID   string     `json:"sessionId"`
	Action      WorkAction `json:"action"`
	ClauseID    string     `json:"clauseId,omitempty"`
	ItemID      string     `json:"itemId,omitempty"`
	Text        string     `json:"text,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	DependsOn   string     `json:"dependsOn,omitempty"`
	UtteranceID string     `json:"utteranceId,omitempty"`
	Handoff     Handoff    `json:"handoff,omitempty"`
}

type WorkResult struct {
	Item     WorkItem `json:"item"`
	Readback string   `json:"readback"`
}

type WorkDepPayload struct {
	ItemID    string `json:"itemId"`
	DependsOn string `json:"dependsOn"`
	Actor     string `json:"actor"`
}

func (s *Store) Work(req WorkRequest, now time.Time) (WorkResult, error) {
	if req.ProjectKey == "" || req.SessionID == "" {
		return WorkResult{}, refuse("project and acting session are required")
	}
	id, readback := req.ItemID, ""
	board, release, err := s.writePlanned(req.ProjectKey, req.SessionID, now, func(board *Board, ids *idIssuer) (EventType, any, error) {
		typ, payload, text, err := board.planWork(req, ids)
		if p, ok := payload.(WorkItemPayload); ok {
			id = p.ItemID
		}
		readback = text
		return typ, payload, err
	})
	if err != nil {
		return WorkResult{}, err
	}
	defer release()
	return WorkResult{Item: *board.workItems[id], Readback: "intent ledger: " + readback}, nil
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
	item := b.workItems[p.ItemID]
	if item == nil {
		return nil
	}
	b.used(p.UtteranceID, sourceUse{kind: RetractedReview, id: p.ItemID, fromState: p.FromState})
	item.State, item.Assignee, item.Reason = p.ToState, p.Assignee, p.Reason
	b.setBy[stateKey(p.ItemID)] = b.applying
	if p.Reviewer != "" {
		item.Reviewer = p.Reviewer
	}
	if p.Handoff != nil {
		item.Handoff = p.Handoff
	}
	return nil
}

func (b *Board) applyWorkDep(data []byte) error {
	var p WorkDepPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if item := b.workItems[p.ItemID]; item != nil {
		item.DependsOn = append(item.DependsOn, p.DependsOn)
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
