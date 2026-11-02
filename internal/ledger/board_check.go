package ledger

import (
	"encoding/json"
	"slices"
	"sort"
	"time"
)

type CheckRun struct {
	Run        string    `json:"run"`
	GoalID     string    `json:"goalId"`
	SessionID  string    `json:"sessionId"`
	Utterances []string  `json:"utterances"`
	StartedAt  time.Time `json:"startedAt"`
	Result     string    `json:"result"`
	Cuts       []string  `json:"cuts,omitempty"`
}

type GoalChecks struct {
	GoalID      string    `json:"goalId"`
	Runs        int       `json:"runs"`
	CutsRaised  int       `json:"cutsRaised"`
	LastCheckAt time.Time `json:"lastCheckAt,omitzero"`
}

type goalCheckState struct {
	GoalChecks
	changedSeq int64
	changedAt  time.Time
	startedSeq int64
}

type checkState struct {
	runs  map[string]*CheckRun
	goals map[string]*goalCheckState
}

func newCheckState() checkState {
	return checkState{runs: map[string]*CheckRun{}, goals: map[string]*goalCheckState{}}
}

func (c checkState) goal(id string) *goalCheckState {
	g := c.goals[id]
	if g == nil {
		g = &goalCheckState{GoalChecks: GoalChecks{GoalID: id}}
		c.goals[id] = g
	}
	return g
}

func (c checkState) checker(sessionID string) bool {
	for _, run := range c.runs {
		if run.SessionID == sessionID {
			return true
		}
	}
	return false
}

func (c checkState) observe(event Event) error {
	switch event.Type {
	case EventGate:
		var p CheckPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		if p.Gate == GateCheck {
			c.applyCheck(p, event)
		}
	case EventAttach:
		var p AttachPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		if p.GoalID != "" {
			c.changed(p.GoalID, event)
		}
	case EventCut:
		var p CutPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		if p.Target != "" && p.CheckRun == "" {
			c.changed(p.Target, event)
		}
	}
	return nil
}

func (c checkState) changed(goalID string, event Event) {
	g := c.goal(goalID)
	g.changedSeq, g.changedAt = event.Seq, event.CreatedAt
}

func (c checkState) applyCheck(p CheckPayload, event Event) {
	g := c.goal(p.GoalID)
	if p.Result == CheckStarted {
		c.runs[p.Run] = &CheckRun{Run: p.Run, GoalID: p.GoalID, SessionID: p.SessionID, Utterances: p.Utterances, StartedAt: event.CreatedAt, Result: CheckStarted}
		g.startedSeq, g.LastCheckAt = event.Seq, event.CreatedAt
		g.Runs++
		return
	}
	if run := c.runs[p.Run]; run != nil {
		run.Result, run.Cuts = p.Result, p.Cuts
		g.CutsRaised += len(p.Cuts)
	}
}

func (b *Board) applyCutWithdrawn(data []byte) error {
	var p CutWithdrawnPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	for _, t := range b.targetCuts {
		if t.ID == p.CutID {
			t.Withdrawn, t.CoveredBy = true, p.ClauseIDs
		}
	}
	return nil
}

func (b *Board) CheckRun(run string) (CheckRun, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r := b.checks.runs[run]
	if r == nil {
		return CheckRun{}, false
	}
	return *r, true
}

func (b *Board) OpenChecks() []CheckRun {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []CheckRun
	for _, r := range b.checks.runs {
		if r.Result == CheckStarted {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func (b *Board) GoalChecks() []GoalChecks {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []GoalChecks
	for _, g := range b.checks.goals {
		if g.Runs > 0 {
			out = append(out, g.GoalChecks)
		}
	}
	sort.Slice(out, func(i, j int) bool { return idNumber(out[i].GoalID) < idNumber(out[j].GoalID) })
	return out
}

func (b *Board) DueChecks(now time.Time, idle time.Duration) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var due []string
	for id, g := range b.checks.goals {
		if b.goals[id] == nil || g.changedSeq <= g.startedSeq || now.Sub(g.changedAt) < idle || b.openCheck(id) {
			continue
		}
		due = append(due, id)
	}
	sort.Slice(due, func(i, j int) bool { return idNumber(due[i]) < idNumber(due[j]) })
	return due
}

func (b *Board) openCheck(goalID string) bool {
	for _, r := range b.checks.runs {
		if r.GoalID == goalID && r.Result == CheckStarted {
			return true
		}
	}
	return false
}

type CheckInput struct {
	GoalID     string          `json:"goalId"`
	GoalName   string          `json:"goalName"`
	Utterances []UtteranceView `json:"utterances"`
	Clauses    []Clause        `json:"clauses"`
	Cuts       []Cut           `json:"cuts,omitempty"`
}

func (b *Board) checkInput(goalID string) (CheckInput, bool) {
	g := b.goals[goalID]
	if g == nil {
		return CheckInput{}, false
	}
	in := CheckInput{GoalID: g.ID, GoalName: g.Name}
	for _, id := range b.utteranceOrder {
		if u := b.utterances[id]; u != nil && slices.Contains(u.GoalIDs, goalID) && personCapture(u.CapturedBy) {
			in.Utterances = append(in.Utterances, *u)
		}
	}
	for _, askID := range g.Asks {
		if ask := b.asks[askID]; ask != nil {
			for _, c := range ask.Clauses {
				in.Clauses = append(in.Clauses, *c)
			}
		}
	}
	for _, c := range g.Constraints {
		in.Clauses = append(in.Clauses, *c)
	}
	for _, t := range b.targetCuts {
		if t.Target == goalID {
			in.Cuts = append(in.Cuts, t.Cut)
		}
	}
	return in, true
}
