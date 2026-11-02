package ledger

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Omission struct {
	Breadth string `json:"breadth"`
	From    string `json:"from"`
}

type CheckReport struct {
	ProjectKey string     `json:"projectKey"`
	Run        string     `json:"run"`
	SessionID  string     `json:"sessionId"`
	Missing    []Omission `json:"missing,omitempty"`
	None       bool       `json:"none,omitempty"`
}

type CheckOutcome struct {
	Run    string   `json:"run"`
	GoalID string   `json:"goalId"`
	Result string   `json:"result"`
	Cuts   []string `json:"cuts,omitempty"`
}

type checkEvent struct {
	typ     EventType
	payload any
}

func (s *Store) writeCheck(projectKey, actor string, now time.Time, plan func(*Board, *idIssuer) ([]checkEvent, error)) error {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return err
	}
	defer release()
	if err := s.commitCheck(board, projectKey, actor, now, plan); err != nil {
		return err
	}
	return s.catchUp(board, projectKey)
}

func (s *Store) commitCheck(board *Board, projectKey, actor string, now time.Time, plan func(*Board, *idIssuer) ([]checkEvent, error)) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	events, err := plan(board, &idIssuer{q: tx, projectKey: projectKey})
	if err != nil {
		return err
	}
	for _, e := range events {
		data, err := json.Marshal(e.payload)
		if err != nil {
			return err
		}
		if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: projectKey, Type: e.typ, Payload: data, ActorSessionID: actor, CreatedAt: now.UTC()}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) StartCheck(projectKey, goalID, run string, now time.Time) (CheckInput, error) {
	var input CheckInput
	err := s.writeCheck(projectKey, run, now, func(board *Board, _ *idIssuer) ([]checkEvent, error) {
		in, ok := board.checkInput(goalID)
		if !ok {
			return nil, refuse("%q is not a goal in %s", goalID, projectKey)
		}
		if board.openCheck(goalID) {
			return nil, refuse("%s already has a check running", goalID)
		}
		if len(in.Utterances) == 0 {
			return nil, refuse("%s has no attached words of the person to check against", goalID)
		}
		if _, used := board.checks.runs[run]; used || run == "" {
			return nil, refuse("check run %q is already recorded", run)
		}
		input = in
		ids := make([]string, len(in.Utterances))
		for i, u := range in.Utterances {
			ids[i] = u.ID
		}
		return []checkEvent{{EventGate, CheckPayload{Gate: GateCheck, Run: run, GoalID: goalID, SessionID: run, Result: CheckStarted, Utterances: ids}}}, nil
	})
	return input, err
}

func (s *Store) ReportCheck(req CheckReport, now time.Time) (CheckOutcome, error) {
	outcome := CheckOutcome{Run: req.Run}
	err := s.writeCheck(req.ProjectKey, req.SessionID, now, func(board *Board, ids *idIssuer) ([]checkEvent, error) {
		run, err := board.openRun(req.Run, req.SessionID)
		if err != nil {
			return nil, err
		}
		if req.None == (len(req.Missing) > 0) {
			return nil, refuse("a check report is either --none or at least one --missing")
		}
		outcome.GoalID, outcome.Result = run.GoalID, CheckNone
		var events []checkEvent
		for _, m := range req.Missing {
			breadth := strings.TrimSpace(m.Breadth)
			if breadth == "" {
				return nil, refuse("a --missing item needs the breadth that is missing")
			}
			if !slices.Contains(run.Utterances, m.From) {
				return nil, refuse("%q is not one of the words this check was given; cite one of %s", m.From, strings.Join(run.Utterances, ", "))
			}
			id, err := ids.next(EventCut, "X")
			if err != nil {
				return nil, err
			}
			outcome.Cuts = append(outcome.Cuts, id)
			events = append(events, checkEvent{EventCut, CutPayload{CutID: id, Target: run.GoalID, AuthorSession: req.SessionID, CheckRun: run.Run, FromUtterance: m.From,
				Reason: fmt.Sprintf("the checker found %q in %s missing from the clauses", breadth, m.From)}})
		}
		if len(outcome.Cuts) > 0 {
			outcome.Result = CheckReported
		}
		return append(events, checkEvent{EventGate, CheckPayload{Gate: GateCheck, Run: run.Run, GoalID: run.GoalID, SessionID: run.SessionID, Result: outcome.Result, Utterances: run.Utterances, Cuts: outcome.Cuts}}), nil
	})
	return outcome, err
}

func (s *Store) FinishCheck(projectKey, run, result, reason string, now time.Time) error {
	return s.writeCheck(projectKey, run, now, func(board *Board, _ *idIssuer) ([]checkEvent, error) {
		r := board.checks.runs[run]
		if r == nil || r.Result != CheckStarted {
			return nil, nil
		}
		return []checkEvent{{EventGate, CheckPayload{Gate: GateCheck, Run: run, GoalID: r.GoalID, SessionID: r.SessionID, Result: result, Utterances: r.Utterances, Reason: reason}}}, nil
	})
}

func (b *Board) openRun(run, sessionID string) (*CheckRun, error) {
	r := b.checks.runs[run]
	switch {
	case r == nil:
		return nil, refuse("%q is not a check run in this project", run)
	case r.SessionID != sessionID:
		return nil, refuse("only the checker session of %s reports it", run)
	case r.Result != CheckStarted:
		return nil, refuse("%s already finished (%s)", run, r.Result)
	}
	return r, nil
}
