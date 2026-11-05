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
	WorkReopen:  {from: []WorkState{WorkDone, WorkClosed}, to: WorkOpen},
	WorkHandoff: {from: []WorkState{WorkInProgress, WorkBlocked}, to: WorkOpen},
	WorkClose:   {from: []WorkState{WorkOpen, WorkInProgress, WorkBlocked, WorkInReview}, to: WorkClosed},
	WorkDelete:  {from: []WorkState{WorkOpen, WorkInProgress, WorkBlocked, WorkInReview, WorkDone, WorkClosed}, to: WorkDeleted},
	WorkRestore: {from: []WorkState{WorkDeleted}, to: WorkOpen},
}

func (b *Board) planWork(req WorkRequest, ids *idIssuer) (EventType, any, string, error) {
	switch req.Action {
	case WorkAdd:
		return b.planWorkAdd(req, ids)
	case WorkDep:
		return b.planWorkDep(req)
	case WorkComment, WorkLog:
		return b.planWorkNote(req)
	case WorkEdit:
		return b.planWorkEdit(req)
	case WorkLink:
		return b.planWorkLink(req)
	}
	transition, ok := workTransitions[req.Action]
	if !ok {
		return "", nil, "", refuse("unknown work action %q; use add, claim, block, unblock, submit, approve, reject, reopen, handoff, dep, comment or log", req.Action)
	}
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	if !slices.Contains(transition.from, item.State) {
		return "", nil, "", refuse("%s is %s; %s needs it %s", item.ID, item.State, req.Action, joinStates(transition.from))
	}
	payload := WorkItemStatePayload{ItemID: item.ID, FromState: item.State, ToState: transition.to, Actor: req.SessionID, Assignee: item.Assignee, Reason: strings.TrimSpace(req.Reason)}
	readback := fmt.Sprintf("%s %s (%s)", item.ID, transition.to, item.origin())
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
		readback = fmt.Sprintf("%s handed off by %s (%s), open for the next session", item.ID, req.SessionID, item.origin())
	case WorkClose:
		word, evidence := closeReason(payload.Reason)
		switch {
		case word == workCloseDone && (item.Assignee != "" || item.State == WorkInReview):
			return "", nil, "", refuse("%s is %s by %s; claimed work is finished through submit and review", item.ID, item.State, item.Assignee)
		case word == workCloseDone && evidence == "":
			return "", nil, "", refuse("close --reason done needs what finished %s, such as \"done: PR #12\" or a commit", item.ID)
		case word == workCloseDone:
			payload.ToState = WorkDone
			readback = fmt.Sprintf("%s done (%s): %s", item.ID, item.origin(), evidence)
		case !slices.Contains(workCloseReasons, word):
			return "", nil, "", refuse("close needs --reason starting with done (and what finished it), %s; claimed work is finished through submit and review", strings.Join(workCloseReasons, ", "))
		}
		payload.Assignee = ""
	case WorkDelete, WorkRestore:
		payload.Assignee = ""
	}
	return EventWorkItemState, payload, readback, nil
}

// planWorkAdd adds asked work under a live clause, or found work with no clause: work a session came across, which never enters the person's intent until it is linked.
func (b *Board) planWorkAdd(req WorkRequest, ids *idIssuer) (EventType, any, string, error) {
	if req.ClauseID != "" {
		if err := b.liveClause(req.ClauseID, req.ProjectKey); err != nil {
			return "", nil, "", err
		}
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return "", nil, "", refuse("a work item needs its text")
	}
	fields, err := req.Fields.normalized()
	if err != nil {
		return "", nil, "", err
	}
	if req.ClauseID != "" && fields.From != "" {
		return "", nil, "", refuse("--clause is asked work and --found-in is found work; give one")
	}
	if fields.From != "" && b.workItems[fields.From] == nil && b.clauses[fields.From] == nil {
		return "", nil, "", refuse("--found-in %q is not a work item or clause in %s", fields.From, req.ProjectKey)
	}
	id, err := ids.next(EventWorkItem, "W")
	if err != nil {
		return "", nil, "", err
	}
	payload := WorkItemPayload{ItemID: id, ClauseID: req.ClauseID, Text: text, CreatedBy: req.SessionID, WorkFields: fields}
	if req.ClauseID == "" {
		return EventWorkItem, payload, fmt.Sprintf("+%s found (%s %s)", id, fields.withDefaults().Type, fields.withDefaults().Priority), nil
	}
	return EventWorkItem, payload, fmt.Sprintf("+%s on %s", id, req.ClauseID), nil
}

func (b *Board) liveClause(id, projectKey string) error {
	c := b.clauses[id]
	if c == nil {
		return refuse("%q is not a clause in %s", id, projectKey)
	}
	if c.State == StateRejected || clauseFullyCut(c) {
		return refuse("%s is no longer in scope; work items hang off live clauses", c.ID)
	}
	return nil
}
