package ledger

import (
	"fmt"
	"strings"
)

func (p *attachPlan) resolveTarget() error {
	req := &p.req
	if req.As == AsAnswers || (req.As == AsReacts && req.Ack) || req.As == AsConversation {
		return nil
	}
	if req.To != "" && req.To != ProjectTarget && !p.board.names(req.To) {
		return refuse("%q is not a goal, ask, clause or work item in %s", req.To, req.ProjectKey)
	}
	if item := p.board.workItems[req.To]; item != nil {
		if item.ClauseID == "" {
			p.retarget(ProjectTarget, "found work "+item.ID+" hangs off no clause, so ")
		} else {
			p.retarget(item.ClauseID, "")
		}
	}
	switch req.As {
	case AsNewAsk:
		return p.toGoal()
	case AsExtends, AsDelegates:
		return p.toAsk()
	case AsConstrains:
		if c := p.board.clauses[req.To]; c != nil {
			p.retarget(p.clauseAnchor(c), "")
		}
		if req.To == "" {
			p.retarget(ProjectTarget, "")
		}
	case AsDecides:
		if ask := p.board.asks[req.To]; ask != nil {
			p.retarget(orProject(ask.GoalID), "")
		}
		if c := p.board.clauses[req.To]; c != nil {
			p.retarget(orProject(p.board.clauseGoal(c)), "")
		}
		if req.To == "" {
			p.retarget(ProjectTarget, "")
		}
	case AsRefines, AsReacts:
		return p.toClause()
	}
	return nil
}

func (b *Board) names(id string) bool {
	return b.goals[id] != nil || b.asks[id] != nil || b.clauses[id] != nil || b.workItems[id] != nil
}

func (p *attachPlan) retarget(to, why string) {
	if to == p.req.To {
		return
	}
	note := fmt.Sprintf("%s--to %s read as %s", why, p.req.To, p.label(to))
	if p.req.To == "" {
		note = fmt.Sprintf("%sno --to, read as %s", why, p.label(to))
	}
	p.notes = append(p.notes, note)
	p.req.To = to
}

func (p *attachPlan) clauseAnchor(c *Clause) string {
	if c.AskID != "" {
		return c.AskID
	}
	return orProject(c.GoalID)
}

func orProject(goalID string) string {
	if goalID == "" {
		return ProjectTarget
	}
	return goalID
}

func (p *attachPlan) isGoal(id string) bool {
	return p.board.goals[id] != nil || p.madeGoal == id && id != ""
}

func (p *attachPlan) toGoal() error {
	req := &p.req
	switch {
	case p.board.asks[req.To] != nil:
		p.retarget(orProject(p.board.asks[req.To].GoalID), "")
	case p.board.clauses[req.To] != nil:
		p.retarget(orProject(p.board.clauseGoal(p.board.clauses[req.To])), "")
	}
	if req.To == "" || req.To == ProjectTarget {
		return p.projectGoal()
	}
	return nil
}

// Words for the whole project need a goal to hang an ask on; one named after the project is made on first use.
func (p *attachPlan) projectGoal() error {
	name := ProjectSlug(p.req.ProjectKey)
	if g := p.board.goalByName(name); g != nil {
		p.retarget(g.ID, "")
		return nil
	}
	id, err := p.ids.next(EventGoal, "G")
	if err != nil {
		return err
	}
	if err := p.add(EventGoal, GoalPayload{GoalID: id, Name: name}); err != nil {
		return err
	}
	p.madeGoal, p.madeGoalName = id, name
	p.effects = append(p.effects, id)
	p.retarget(id, "")
	p.notes[len(p.notes)-1] += ", a goal made for the project"
	return nil
}

func (p *attachPlan) toAsk() error {
	req := &p.req
	if c := p.board.clauses[req.To]; c != nil {
		p.retarget(p.clauseAnchor(c), "")
	}
	if req.To == "" || req.To == ProjectTarget {
		if err := p.projectGoal(); err != nil {
			return err
		}
	}
	if !p.isGoal(req.To) {
		return nil
	}
	var asks []string
	if g := p.board.goals[req.To]; g != nil {
		asks = g.Asks
	}
	if len(asks) == 1 {
		p.retarget(asks[0], "")
		return nil
	}
	if req.As == AsDelegates {
		if len(asks) == 0 {
			return refuse("%s has no ask to delegate; attach the work first with --as new-ask --to %s", p.label(req.To), req.To)
		}
		return refuse("%s has %d asks; name the one to delegate: %s", p.label(req.To), len(asks), strings.Join(asks, ", "))
	}
	held := "no ask"
	if len(asks) > 1 {
		held = fmt.Sprintf("%d asks", len(asks))
	}
	p.notes = append(p.notes, fmt.Sprintf("%s has %s, so --as extends became a new ask on it", p.label(req.To), held))
	req.As = AsNewAsk
	return nil
}

func (p *attachPlan) toClause() error {
	req := &p.req
	ask := p.board.asks[req.To]
	if ask == nil || len(ask.Clauses) == 0 {
		return nil
	}
	if req.As == AsReacts && !req.Adopt && !req.Reject && len(ask.Clauses) > 1 {
		return nil
	}
	if len(ask.Clauses) == 1 {
		p.retarget(ask.Clauses[0].ID, "")
		return nil
	}
	ids := make([]string, 0, len(ask.Clauses))
	for _, c := range ask.Clauses {
		ids = append(ids, c.ID)
	}
	return refuse("--as %s takes a clause and %s has %d; name one: %s", req.As, ask.ID, len(ids), strings.Join(ids, ", "))
}
