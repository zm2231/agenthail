package ledger

import (
	"fmt"
	"slices"
	"strings"
)

func (p *attachPlan) addClauses() error {
	req := p.req
	source := req.Source
	if source == "" {
		source = SourceStated
	}
	if len(req.Clauses) == 0 {
		return refuse("--as %s needs at least one --clause", req.As)
	}
	for _, text := range req.Clauses {
		if strings.TrimSpace(text) == "" {
			return refuse("a --clause is empty")
		}
	}
	askID, kind, err := p.clauseTarget()
	if err != nil {
		return err
	}
	owner, proposedBy, err := p.provenance(source, askID)
	if err != nil {
		return err
	}
	if kind == ClauseKindConstraint && askID == "" {
		owner = ""
	}
	if req.As == AsNewAsk {
		if askID, err = p.newAsk(source); err != nil {
			return err
		}
	}
	var ids []string
	for _, text := range req.Clauses {
		id, err := p.ids.next(EventClause, "C")
		if err != nil {
			return err
		}
		ids = append(ids, id)
		if err := p.add(EventClause, ClausePayload{
			ClauseID: id, AskID: askID, GoalID: p.goalID, Kind: kind, Text: strings.TrimSpace(text),
			State: StateCaptured, Source: source, OwnerSessionID: owner, ProposedBy: proposedBy, SourceUtteranceID: p.utteranceID(),
		}); err != nil {
			return err
		}
	}
	p.effects = append(p.effects, ids...)
	p.summary = p.clauseSummary(askID, kind, ids, source)
	return p.cover(ids, source)
}

func (p *attachPlan) clauseTarget() (string, string, error) {
	req := p.req
	switch req.As {
	case AsNewAsk:
		if !p.isGoal(req.To) {
			return "", "", refuse("--as new-ask needs --to <goal>; %q is not a goal in %s (create one with: agenthail ledger goal <name>)", req.To, req.ProjectKey)
		}
		p.goalID = req.To
		return "", "", nil
	case AsExtends:
		ask := p.board.asks[req.To]
		if ask == nil {
			return "", "", refuse("--as extends needs --to <ask>; %q is not an ask in %s", req.To, req.ProjectKey)
		}
		p.goalID = ask.GoalID
		return ask.ID, "", nil
	}
	switch {
	case req.To == ProjectTarget:
		return "", ClauseKindConstraint, nil
	case p.board.asks[req.To] != nil:
		p.goalID = p.board.asks[req.To].GoalID
		return req.To, ClauseKindConstraint, nil
	case p.board.goals[req.To] != nil:
		p.goalID = req.To
		return "", ClauseKindConstraint, nil
	}
	return "", "", refuse("--as constrains needs --to <ask|goal|project>; %q is none of these in %s", req.To, req.ProjectKey)
}

func (p *attachPlan) provenance(source ClauseSource, askID string) (string, string, error) {
	switch source {
	case SourceStated, SourceInferred:
		if p.utterance == nil {
			return "", "", refuse("%s clauses need the person's captured utterance", source)
		}
		return p.utterance.TargetID, "", nil
	case SourceProposed:
		return "", p.req.SessionID, nil
	case SourceDelegated:
		ask := p.board.asks[askID]
		if ask == nil || !slices.Contains(ask.Delegates, p.req.SessionID) {
			return "", "", refuse("delegated clauses need an ask the person delegated to %s", p.req.SessionID)
		}
		return p.req.SessionID, "", nil
	case SourceAdopted:
		return "", "", refuse("adopt a proposal with --as reacts --to <clause> --adopt")
	}
	return "", "", refuse("unknown --source %q; use stated, inferred, proposed or delegated", source)
}

func (p *attachPlan) newAsk(source ClauseSource) (string, error) {
	askType := p.req.AskType
	if askType == "" {
		askType = AskBuild
	}
	if askType != AskBuild && askType != AskInquiry && askType != AskDecision {
		return "", refuse("unknown ask type %q; use build, inquiry or decision", askType)
	}
	if source == SourceDelegated {
		return "", refuse("a new ask cannot be delegated before it exists; attach it as stated, then --as delegates")
	}
	id, err := p.ids.next(EventAsk, "A")
	if err != nil {
		return "", err
	}
	p.effects = append(p.effects, id)
	return id, p.add(EventAsk, AskPayload{AskID: id, GoalID: p.goalID, Type: askType, Principal: p.speaker(), SourceUtterance: p.utteranceID()})
}

func (p *attachPlan) clauseSummary(askID, kind string, ids []string, source ClauseSource) string {
	noun := "clause"
	if kind == ClauseKindConstraint {
		noun = "constraint"
	}
	if len(ids) != 1 {
		noun += "s"
	}
	on := askID
	if on == "" {
		on = p.label(p.goalID)
	}
	text := fmt.Sprintf("+%d %s on %s (%s)", len(ids), noun, on, strings.Join(ids, ", "))
	if p.req.As == AsNewAsk {
		text = fmt.Sprintf("+%s on %s with %d %s (%s)", askID, p.label(p.goalID), len(ids), noun, strings.Join(ids, ", "))
	}
	if source == SourceProposed {
		text += ", proposed: binds once the person adopts it"
	}
	return text
}
