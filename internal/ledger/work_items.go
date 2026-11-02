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
	WorkComment WorkAction = "comment"
	WorkLog     WorkAction = "log"
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
	ID        string      `json:"id"`
	ClauseID  string      `json:"clauseId"`
	Text      string      `json:"text"`
	State     WorkState   `json:"state"`
	Assignee  string      `json:"assignee,omitempty"`
	CreatedBy string      `json:"createdBy"`
	Reason    string      `json:"reason,omitempty"`
	Reviewer  string      `json:"reviewer,omitempty"`
	DependsOn []string    `json:"dependsOn,omitempty"`
	Handoff   *Handoff    `json:"handoff,omitempty"`
	Timeline  []WorkEntry `json:"timeline,omitempty"`
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
	LogType     string     `json:"logType,omitempty"`
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
	item, _ := board.WorkItem(id)
	return WorkResult{Item: item, Readback: "intent ledger: " + readback}, nil
}

func joinStates(states []WorkState) string {
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = string(s)
	}
	return strings.Join(names, " or ")
}

func (b *Board) applyWorkItem(event Event) error {
	var p WorkItemPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item := &WorkItem{ID: p.ItemID, ClauseID: p.ClauseID, Text: p.Text, State: WorkOpen, CreatedBy: p.CreatedBy,
		Timeline: []WorkEntry{{At: event.CreatedAt, Actor: p.CreatedBy, Kind: "created", Text: p.Text, State: WorkOpen}}}
	b.workItems[p.ItemID] = item
	if c := b.clauses[p.ClauseID]; c != nil {
		c.WorkItems = append(c.WorkItems, item)
	}
	return nil
}

func (b *Board) applyWorkItemState(event Event) error {
	var p WorkItemStatePayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item := b.workItems[p.ItemID]
	if item == nil {
		return nil
	}
	item.State, item.Assignee, item.Reason = p.ToState, p.Assignee, p.Reason
	if p.Reviewer != "" {
		item.Reviewer = p.Reviewer
	}
	if p.Handoff != nil {
		item.Handoff = p.Handoff
	}
	item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "state", State: p.ToState, Text: stateEntryText(p)})
	return nil
}

func stateEntryText(p WorkItemStatePayload) string {
	var parts []string
	if p.Reason != "" {
		parts = append(parts, p.Reason)
	}
	if p.Reviewer != "" {
		parts = append(parts, "reviewed by "+p.Reviewer)
	}
	if h := p.Handoff; h != nil {
		parts = append(parts, fmt.Sprintf("handoff: done %q, remaining %q, decisions %q, uncertain %q", h.Done, h.Remaining, h.Decisions, h.Uncertain))
	}
	return strings.Join(parts, "; ")
}

func (b *Board) applyWorkDep(event Event) error {
	var p WorkDepPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	if item := b.workItems[p.ItemID]; item != nil {
		item.DependsOn = append(item.DependsOn, p.DependsOn)
		item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "dep", Text: "waits on " + p.DependsOn})
	}
	return nil
}

func (b *Board) WorkItems() []WorkItem {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]WorkItem, 0, len(b.workItems))
	for _, item := range b.workItems {
		out = append(out, item.snapshot())
	}
	return out
}
