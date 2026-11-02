package ledger

import (
	"fmt"
	"slices"
	"strings"
)

var workTransitions = map[WorkAction]struct {
	from []WorkState
	to   WorkState
}{
	WorkClaim:   {from: []WorkState{WorkOpen}, to: WorkInProgress},
	WorkBlock:   {from: []WorkState{WorkOpen, WorkInProgress}, to: WorkBlocked},
	WorkUnblock: {from: []WorkState{WorkBlocked}, to: WorkOpen},
	WorkSubmit:  {from: []WorkState{WorkInProgress}, to: WorkInReview},
	WorkApprove: {from: []WorkState{WorkInReview}, to: WorkDone},
	WorkReject:  {from: []WorkState{WorkInReview}, to: WorkInProgress},
	WorkReopen:  {from: []WorkState{WorkDone}, to: WorkOpen},
	WorkHandoff: {from: []WorkState{WorkInProgress, WorkBlocked}, to: WorkOpen},
}

func (b *Board) planWork(req WorkRequest, ids *idIssuer) (EventType, any, string, error) {
	switch req.Action {
	case WorkAdd:
		return b.planWorkAdd(req, ids)
	case WorkDep:
		return b.planWorkDep(req)
	}
	transition, ok := workTransitions[req.Action]
	if !ok {
		return "", nil, "", refuse("unknown work action %q; use add, claim, block, unblock, submit, approve, reject, reopen, handoff or dep", req.Action)
	}
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	if !slices.Contains(transition.from, item.State) {
		return "", nil, "", refuse("%s is %s; %s needs it %s", item.ID, item.State, req.Action, joinStates(transition.from))
	}
	payload := WorkItemStatePayload{ItemID: item.ID, FromState: item.State, ToState: transition.to, Actor: req.SessionID, Assignee: item.Assignee, Reason: strings.TrimSpace(req.Reason)}
	readback := fmt.Sprintf("%s %s (%s)", item.ID, transition.to, item.ClauseID)
	switch req.Action {
	case WorkClaim:
		if open := b.openDeps(item); len(open) > 0 {
			return "", nil, "", refuse("%s waits on %s; claim it once they are done", item.ID, strings.Join(open, ", "))
		}
		payload.Assignee = req.SessionID
	case WorkBlock:
		if payload.Reason == "" {
			return "", nil, "", refuse("block needs --reason: what %s waits on", item.ID)
		}
	case WorkSubmit:
		if item.Assignee != req.SessionID {
			return "", nil, "", refuse("%s is claimed by %s; only its assignee submits it", item.ID, item.Assignee)
		}
	case WorkApprove, WorkReject:
		reviewer, err := b.reviewer(req, item)
		if err != nil {
			return "", nil, "", err
		}
		if req.Action == WorkReject && payload.Reason == "" {
			return "", nil, "", refuse("reject needs --reason: what %s still needs", item.ID)
		}
		payload.Reviewer, payload.UtteranceID = reviewer, req.UtteranceID
		readback += " reviewed by " + reviewer
	case WorkHandoff:
		if item.Assignee != req.SessionID {
			return "", nil, "", refuse("%s is claimed by %s; only its assignee hands it off", item.ID, item.Assignee)
		}
		if req.Handoff.empty() {
			return "", nil, "", refuse("a handoff needs at least one of --done, --remaining, --decisions or --uncertain")
		}
		handoff := req.Handoff
		handoff.By = req.SessionID
		payload.Handoff, payload.Assignee = &handoff, ""
		readback = fmt.Sprintf("%s handed off by %s (%s), open for the next session", item.ID, req.SessionID, item.ClauseID)
	}
	return EventWorkItemState, payload, readback, nil
}

func (b *Board) planWorkAdd(req WorkRequest, ids *idIssuer) (EventType, any, string, error) {
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
