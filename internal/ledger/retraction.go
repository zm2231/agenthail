package ledger

import (
	"fmt"
	"slices"
)

const (
	RetractedAsk        = "ask"
	RetractedClause     = "clause"
	RetractedAdoption   = "adoption"
	RetractedDecision   = "decision"
	RetractedAnswer     = "answer"
	RetractedReview     = "review"
	RetractedCutAck     = "cut-ack"
	RetractedCutCover   = "cut-withdrawal"
	RetractedDelegation = "delegation"
	RetractedRevision   = "revision"
	RetractedRejection  = "rejection"
	RetractedCut        = "cut"
)

type Retraction struct {
	Kind        string   `json:"kind"`
	ID          string   `json:"id"`
	UtteranceID string   `json:"utteranceId"`
	Reason      string   `json:"reason"`
	AttachedBy  []string `json:"attachedBy,omitempty"`
	// Outward marks an answer or approval that may already have let a session merge, deploy or send.
	Outward bool `json:"authorizedOutward,omitempty"`
}

type sourceUse struct {
	kind      string
	id        string
	prior     string
	owners    map[string]string
	delegate  string
	fromState WorkState
}

func (b *Board) used(ref string, use sourceUse) {
	if ref != "" {
		b.uses[ref] = append(b.uses[ref], use)
	}
}

func (r Retraction) String() string {
	line := fmt.Sprintf("retracted: %s %s built from %s words, not the person's (%s)", r.Kind, r.ID, r.Reason, r.UtteranceID)
	if r.Outward {
		line += "; it authorized an outward act: check whether a merge, deploy or send was done on it"
	}
	return line
}

func (b *Board) Retractions() []Retraction {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return slices.Clone(b.retractions)
}

func (b *Board) retract(ref, reason string) {
	uses := b.uses[ref]
	delete(b.uses, ref)
	for i := len(uses) - 1; i >= 0; i-- {
		use := uses[i]
		r := Retraction{Kind: use.kind, ID: use.id, UtteranceID: ref, Reason: reason, AttachedBy: b.attachedBy[ref]}
		if !b.undo(ref, use, &r) {
			continue
		}
		b.retractions = append(b.retractions, r)
	}
}

func (b *Board) undo(ref string, use sourceUse, r *Retraction) bool {
	switch use.kind {
	case RetractedAsk:
		ask := b.asks[use.id]
		if ask == nil {
			return false
		}
		for _, c := range ask.Clauses {
			delete(b.clauses, c.ID)
			b.retractions = append(b.retractions, Retraction{Kind: RetractedClause, ID: c.ID, UtteranceID: ref, Reason: r.Reason, AttachedBy: r.AttachedBy})
		}
		delete(b.asks, use.id)
		if g := b.goals[ask.GoalID]; g != nil {
			g.Asks = slices.DeleteFunc(g.Asks, func(id string) bool { return id == use.id })
		}
	case RetractedClause:
		c := b.clauses[use.id]
		if c == nil {
			return false
		}
		c.Sources = withoutRef(c.Sources, ref)
		if len(c.Sources) > 0 || c.Source == SourceProposed {
			return false
		}
		b.dropClause(c)
	case RetractedAdoption:
		c := b.clauses[use.id]
		if c == nil {
			return false
		}
		c.Sources = withoutRef(c.Sources, ref)
		c.Source = ClauseSource(use.prior)
	case RetractedDecision:
		d := b.decisions[use.id]
		if d == nil {
			return false
		}
		if q := b.questions[d.QuestionID]; q != nil && q.Kind == QuestionOutward {
			r.Outward = true
		}
		delete(b.decisions, use.id)
	case RetractedAnswer:
		q := b.questions[use.id]
		if q == nil || q.AnsweredBy != ref {
			return false
		}
		q.State, q.Answer, q.AnsweredBy, q.Applied = QuestionOpen, "", "", false
		r.Outward = q.Kind == QuestionOutward
		b.restoreQuestionCuts(q.ID)
	case RetractedReview:
		item := b.workItems[use.id]
		if item == nil {
			return false
		}
		item.State, item.Reviewer = use.fromState, ""
		delete(b.reviews.approvedBy, ref)
		r.Outward = true
	case RetractedCutAck:
		return b.setCut(use.id, func(c *Cut) { c.Acked = false })
	case RetractedCutCover:
		return b.setCut(use.id, func(c *Cut) { c.Withdrawn, c.CoveredBy = false, nil })
	case RetractedCut:
		return b.setCut(use.id, func(c *Cut) { c.Withdrawn = true })
	case RetractedRejection:
		c := b.clauses[use.id]
		if c == nil || c.State != StateRejected {
			return false
		}
		c.State = ClauseState(use.prior)
	case RetractedDelegation:
		ask := b.asks[use.id]
		if ask == nil {
			return false
		}
		if i := slices.Index(ask.Delegates, use.delegate); i >= 0 {
			ask.Delegates = slices.Delete(ask.Delegates, i, i+1)
		}
		for id, owner := range use.owners {
			if c := b.clauses[id]; c != nil {
				c.OwnerSessionID = owner
			}
		}
	case RetractedRevision:
		c := b.clauses[use.id]
		if c == nil {
			return false
		}
		c.Text = use.prior
		c.Sources = withoutRef(c.Sources, ref)
	default:
		return false
	}
	return true
}

func (b *Board) dropClause(c *Clause) {
	delete(b.clauses, c.ID)
	if ask := b.asks[c.AskID]; ask != nil {
		ask.Clauses = slices.DeleteFunc(ask.Clauses, func(x *Clause) bool { return x.ID == c.ID })
	}
	if g := b.goals[c.GoalID]; g != nil {
		g.Constraints = slices.DeleteFunc(g.Constraints, func(x *Clause) bool { return x.ID == c.ID })
	}
	b.projectConstraints = slices.DeleteFunc(b.projectConstraints, func(x *Clause) bool { return x.ID == c.ID })
}

func (b *Board) setCut(id string, change func(*Cut)) bool {
	for _, c := range b.clauses {
		for i := range c.Cuts {
			if c.Cuts[i].ID == id {
				change(&c.Cuts[i])
				return true
			}
		}
	}
	for _, t := range b.targetCuts {
		if t.ID == id {
			change(&t.Cut)
			return true
		}
	}
	return false
}

func (b *Board) restoreQuestionCuts(questionID string) {
	restore := func(c *Cut) {
		if c.QuestionID == questionID && !c.Acked && len(c.CoveredBy) == 0 {
			c.Withdrawn = false
		}
	}
	for _, c := range b.clauses {
		for i := range c.Cuts {
			restore(&c.Cuts[i])
		}
	}
	for _, t := range b.targetCuts {
		restore(&t.Cut)
	}
}

func withoutRef(refs []string, ref string) []string {
	return slices.DeleteFunc(slices.Clone(refs), func(r string) bool { return r == ref })
}
