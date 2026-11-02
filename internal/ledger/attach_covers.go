package ledger

import "strings"

func (p *attachPlan) cover(clauseIDs []string, source ClauseSource) error {
	if len(p.req.Covers) == 0 {
		return nil
	}
	if p.utterance == nil || (source != SourceStated && source != SourceInferred) {
		return refuse("--covers withdraws a checker's cut only with a stated or inferred clause from the person's words")
	}
	for _, id := range p.req.Covers {
		cut := p.board.targetCut(id)
		switch {
		case cut == nil || cut.CheckRun == "":
			return refuse("%q is not a checker's cut in %s", id, p.req.ProjectKey)
		case cut.Acked || cut.Withdrawn:
			return refuse("%s is already %s", id, map[bool]string{true: "acked", false: "withdrawn"}[cut.Acked])
		case cut.Target != p.goalID:
			return refuse("%s is a cut on %s, not on %s", id, cut.Target, p.label(p.goalID))
		case cut.FromUtterance != p.utterance.ID:
			return refuse("%s was raised from %s; cover it with a clause from those words", id, cut.FromUtterance)
		}
		if err := p.add(EventCutWithdrawn, CutWithdrawnPayload{CutID: id, ClauseIDs: clauseIDs, UtteranceID: p.utterance.ID, SessionID: p.req.SessionID}); err != nil {
			return err
		}
		p.effects = append(p.effects, id+" covered")
	}
	return nil
}

func (c Cut) WithdrawnBecause() string {
	if len(c.CoveredBy) > 0 {
		return "covered by " + strings.Join(c.CoveredBy, ", ")
	}
	return "its question was answered"
}

func (b *Board) targetCut(id string) *TargetCut {
	for _, t := range b.targetCuts {
		if t.ID == id {
			return t
		}
	}
	return nil
}
