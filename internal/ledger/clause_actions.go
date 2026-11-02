package ledger

import (
	"fmt"
	"strings"
	"time"
)

type ClauseAction string

const (
	ClauseOwn      ClauseAction = "own"
	ClauseProgress ClauseAction = "progress"
	ClauseWait     ClauseAction = "wait"
)

type ClauseRequest struct {
	ProjectKey string       `json:"projectKey"`
	SessionID  string       `json:"sessionId"`
	Action     ClauseAction `json:"action"`
	ClauseID   string       `json:"clauseId"`
	Owner      string       `json:"owner,omitempty"`
	Ref        string       `json:"ref,omitempty"`
	Condition  string       `json:"condition,omitempty"`
	DeadlineMs int64        `json:"deadlineMs,omitempty"`
}

type ClauseResult struct {
	Clause   Clause `json:"clause"`
	Readback string `json:"readback"`
}

func (s *Store) ClauseAction(req ClauseRequest, now time.Time) (ClauseResult, error) {
	if req.ProjectKey == "" || req.SessionID == "" {
		return ClauseResult{}, refuse("project and acting session are required")
	}
	var readback string
	board, release, err := s.writePlanned(req.ProjectKey, req.SessionID, now, func(board *Board, _ *idIssuer) (EventType, any, error) {
		typ, payload, text, err := board.planClauseAction(req, now)
		readback = text
		return typ, payload, err
	})
	if err != nil {
		return ClauseResult{}, err
	}
	defer release()
	return ClauseResult{Clause: *board.clauses[req.ClauseID], Readback: "intent ledger: " + readback}, nil
}

func (b *Board) planClauseAction(req ClauseRequest, now time.Time) (EventType, any, string, error) {
	c := b.clauses[req.ClauseID]
	switch {
	case c == nil:
		return "", nil, "", refuse("%q is not a clause in %s", req.ClauseID, req.ProjectKey)
	case c.State == StateRejected || clauseFullyCut(c):
		return "", nil, "", refuse("%s is no longer in scope", c.ID)
	case c.Source == SourceProposed:
		return "", nil, "", refuse("%s is a proposal; it binds no one until the person adopts it", c.ID)
	case c.State == StateReported:
		return "", nil, "", refuse("%s is already reported", c.ID)
	}
	if req.Action == ClauseOwn {
		owner := strings.TrimSpace(req.Owner)
		if owner == "" {
			owner = req.SessionID
		}
		to := c.State
		if to == StateCaptured || to == StateReadBack {
			to = StateOwned
		}
		return EventClauseState, ClauseStatePayload{ClauseID: c.ID, FromState: c.State, ToState: to, OwnerSessionID: owner, Actor: req.SessionID},
			fmt.Sprintf("%s owned by %s", c.ID, owner), nil
	}
	if c.OwnerSessionID != req.SessionID {
		owner := c.OwnerSessionID
		if owner == "" {
			owner = "no one"
		}
		return "", nil, "", refuse("%s is owned by %s; own it first with: agenthail ledger own %s", c.ID, owner, c.ID)
	}
	switch req.Action {
	case ClauseProgress:
		ref := strings.TrimSpace(req.Ref)
		if ref == "" {
			return "", nil, "", refuse("progress needs --ref: the commit, file, PR or evidence that changed")
		}
		return EventClauseState, ClauseStatePayload{ClauseID: c.ID, FromState: c.State, ToState: c.State, ProgressRef: ref, Actor: req.SessionID},
			fmt.Sprintf("%s progress (self-reported): %s", c.ID, ref), nil
	case ClauseWait:
		condition := strings.TrimSpace(req.Condition)
		if condition == "" || req.DeadlineMs <= now.UnixMilli() {
			return "", nil, "", refuse("a wait needs --condition and a --deadline in the future")
		}
		return EventWait, WaitPayload{ClauseID: c.ID, SessionID: req.SessionID, Condition: condition, DeadlineMs: req.DeadlineMs},
			fmt.Sprintf("%s waits on %s until %s", c.ID, condition, time.UnixMilli(req.DeadlineMs).UTC().Format(time.RFC3339)), nil
	}
	return "", nil, "", refuse("unknown clause action %q; use own, progress or wait", req.Action)
}
