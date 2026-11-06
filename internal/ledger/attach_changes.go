package ledger

import (
	"fmt"
	"strings"
)

func (p *attachPlan) targetClause() (*Clause, error) {
	c := p.board.clauses[p.req.To]
	if c == nil {
		return nil, refuse("--as %s needs --to <clause>; %q is not a clause in %s", p.req.As, p.req.To, p.req.ProjectKey)
	}
	if c.State == StateRejected {
		return nil, refuse("%s was rejected", c.ID)
	}
	p.goalID = p.board.clauseGoal(c)
	return c, nil
}

func (p *attachPlan) singleText(what string) (string, error) {
	if len(p.req.Clauses) != 1 || strings.TrimSpace(p.req.Clauses[0]) == "" {
		return "", refuse("--as %s takes exactly one --clause: %s", p.req.As, what)
	}
	return strings.TrimSpace(p.req.Clauses[0]), nil
}

func (p *attachPlan) refine() error {
	c, err := p.targetClause()
	if err != nil {
		return err
	}
	text, err := p.singleText("the new wording")
	if err != nil {
		return err
	}
	p.effects = append(p.effects, c.ID)
	p.summary = c.ID + " refined"
	return p.add(EventSupersession, SupersessionPayload{ClauseID: c.ID, OldVersion: c.Text, NewVersion: text, SourceUtteranceID: p.utterance.ID})
}

func (p *attachPlan) react() error {
	chosen := 0
	for _, flag := range []bool{p.req.Adopt, p.req.Reject, p.req.Ack} {
		if flag {
			chosen++
		}
	}
	if chosen > 1 {
		return refuse("--as reacts takes one of --adopt, --reject or --ack, not several")
	}
	if len(p.req.Clauses) > 0 {
		defer func() { p.summary += "; the --clause was not recorded: a reaction keeps the wording (reword it with --as refines)" }()
	}
	if p.req.Ack {
		return p.ackCut()
	}
	if chosen == 0 {
		return p.plainReaction()
	}
	c, err := p.targetClause()
	if err != nil {
		return err
	}
	p.effects = append(p.effects, c.ID)
	if p.req.Adopt {
		if c.Source != SourceProposed {
			return refuse("only a proposed clause can be adopted; %s is %s", c.ID, c.Source)
		}
		owner := ""
		if c.AskID != "" {
			owner = p.utterance.TargetID
		}
		p.summary = c.ID + " adopted"
		return p.add(EventClauseSource, ClauseSourcePayload{ClauseID: c.ID, FromSource: c.Source, ToSource: SourceAdopted, UtteranceID: p.utterance.ID, OwnerSessionID: owner})
	}
	if c.Source == SourceProposed {
		p.summary = c.ID + " rejected"
		return p.add(EventClauseState, ClauseStatePayload{ClauseID: c.ID, FromState: c.State, ToState: StateRejected, EvidenceRef: p.utterance.ID, Actor: p.req.SessionID})
	}
	id, err := p.ids.next(EventCut, "X")
	if err != nil {
		return err
	}
	p.effects = append(p.effects, id)
	p.summary = fmt.Sprintf("%s cut, pending the person's ack (%s)", c.ID, id)
	return p.add(EventCut, CutPayload{CutID: id, ClauseID: c.ID, Reason: "rejected in " + p.utterance.ID, AuthorSession: p.req.SessionID, FromUtterance: p.utterance.ID})
}

func (p *attachPlan) plainReaction() error {
	if p.req.To == "" {
		p.summary = p.utterance.ID + " is a reaction; nothing changed"
		return nil
	}
	c, err := p.targetClause()
	if err != nil {
		return err
	}
	p.effects = append(p.effects, c.ID)
	p.summary = fmt.Sprintf("%s is a reaction to %s; nothing changed (add --adopt or --reject to change it)", p.utterance.ID, c.ID)
	return nil
}

func (p *attachPlan) ackCut() error {
	for _, c := range p.board.clauses {
		for _, cut := range c.Cuts {
			if cut.ID == p.req.To {
				p.goalID = p.board.clauseGoal(c)
				return p.recordAck(cut, c.ID)
			}
		}
	}
	for _, t := range p.board.targetCuts {
		if t.ID == p.req.To {
			goalID, err := p.board.targetGoal(t.Target)
			if err != nil {
				return err
			}
			p.goalID = goalID
			return p.recordAck(t.Cut, t.Target)
		}
	}
	return refuse("--as reacts --ack needs --to <cut>; %q is not a cut in %s", p.req.To, p.req.ProjectKey)
}

func (p *attachPlan) recordAck(cut Cut, target string) error {
	if cut.Acked {
		return refuse("%s is already acked", cut.ID)
	}
	if cut.Withdrawn {
		return refuse("%s was withdrawn: %s", cut.ID, cut.WithdrawnBecause())
	}
	p.effects = append(p.effects, cut.ID, target)
	p.summary = fmt.Sprintf("%s acked: %s is cut", cut.ID, target)
	if err := p.add(EventCutAck, CutAckPayload{CutID: cut.ID, AckedBy: p.utterance.SpeakerID, SourceUtteranceID: p.utterance.ID}); err != nil {
		return err
	}
	q := p.board.questions[cut.QuestionID]
	if q == nil || q.State != QuestionOpen {
		return nil
	}
	p.summary += fmt.Sprintf("; %s resolved with its default", q.ID)
	return p.add(EventQuestionRes, QuestionResolvedPayload{QuestionID: q.ID, Answer: q.DefaultAnswer, SourceUtteranceID: p.utterance.ID, Applied: true, Effects: []string{cut.ID}})
}

func (p *attachPlan) delegate() error {
	ask := p.board.asks[p.req.To]
	if ask == nil {
		return refuse("--as delegates needs --to <ask>; %q is not an ask in %s", p.req.To, p.req.ProjectKey)
	}
	if len(p.req.Clauses) > 0 {
		return refuse("--as delegates takes no --clause; fill the ask with --source delegated afterwards")
	}
	delegate := p.req.Delegate
	if delegate == "" {
		delegate = p.req.SessionID
	}
	p.goalID = ask.GoalID
	p.effects = append(p.effects, ask.ID)
	p.summary = fmt.Sprintf("%s delegated to %s", ask.ID, delegate)
	return p.add(EventDelegation, DelegationPayload{AskID: ask.ID, DelegatorSessionID: p.utterance.SpeakerID, DelegateSessionID: delegate, SourceUtteranceID: p.utterance.ID})
}

func (p *attachPlan) decide() error {
	switch {
	case p.req.To == ProjectTarget:
	case p.board.goals[p.req.To] != nil:
		p.goalID = p.req.To
	default:
		return refuse("--as decides needs --to <goal|project>; %q is neither in %s", p.req.To, p.req.ProjectKey)
	}
	text, err := p.singleText("the decision")
	if err != nil {
		return err
	}
	id, err := p.ids.next(EventDecision, "D")
	if err != nil {
		return err
	}
	p.effects = append(p.effects, id)
	p.summary = fmt.Sprintf("+%s decided on %s", id, p.label(p.goalID))
	return p.add(EventDecision, DecisionPayload{DecisionID: id, GoalID: p.goalID, Version: 1, Text: text, Principal: p.utterance.SpeakerID, SourceUtterance: p.utterance.ID})
}
