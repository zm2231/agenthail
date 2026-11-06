package ledger

import "fmt"

func (p *attachPlan) inferAs() error {
	req := &p.req
	if req.As != "" {
		return nil
	}
	words := len(req.Clauses) > 0
	kind := p.board.kindOf(req.To)
	var why string
	switch {
	case req.Delegate != "":
		req.As, why = AsDelegates, "--session names a delegate"
	case kind == "cut" && !req.Ack:
		return refuse("%s is a cut; acking it drops what it cuts for good, so give --ack to ack it", req.To)
	case req.Ack:
		req.As, why = AsReacts, "--ack was given"
	case req.Adopt || req.Reject:
		req.As, why = AsReacts, "--adopt or --reject was given"
	case kind == "question" && words && p.board.asks[p.board.questions[req.To].Target] == nil:
		return refuse("an answer to %s with a --clause %s, so give --as answers to apply it", req.To, p.answerChange(p.board.questions[req.To]))
	case kind == "question":
		req.As, why = AsAnswers, "--to is a question"
	case !words && req.To == "":
		req.As, why = AsConversation, "no --to and no --clause"
	case !words:
		req.As, why = AsReacts, "no --clause"
	case kind == "clause" && p.board.workItems[req.To] != nil:
		req.As, why = AsExtends, "--to "+req.To+" serves a clause, and new words add to it; --as refines rewords it"
	case kind == "clause":
		req.As, why = AsExtends, "--to is a clause, and new words add to it; --as refines rewords it"
	case kind == "ask":
		req.As, why = AsExtends, "--to is an ask"
	case req.To == "":
		req.As, why = AsNewAsk, "no --to"
	case kind == ProjectTarget:
		req.As, why = AsNewAsk, "--to is the project"
	default:
		req.As, why = AsNewAsk, "--to is a "+kind
	}
	p.notes = append(p.notes, fmt.Sprintf("no --as, read as %s: %s", req.As, why))
	return nil
}

func (p *attachPlan) answerChange(q *Question) string {
	if p.board.clauses[q.Target] != nil {
		return "rewords " + q.Target
	}
	return "records a decision on " + p.label(q.GoalID)
}

func (b *Board) kindOf(id string) string {
	if item := b.workItems[id]; item != nil {
		if item.ClauseID == "" {
			return "found work item"
		}
		id = item.ClauseID
	}
	switch {
	case id == ProjectTarget:
		return ProjectTarget
	case b.clauses[id] != nil:
		return "clause"
	case b.asks[id] != nil:
		return "ask"
	case b.goals[id] != nil:
		return "goal"
	case b.questions[id] != nil:
		return "question"
	case b.isCut(id):
		return "cut"
	}
	return ""
}

func (b *Board) isCut(id string) bool {
	for _, c := range b.clauses {
		for _, cut := range c.Cuts {
			if cut.ID == id {
				return true
			}
		}
	}
	for _, t := range b.targetCuts {
		if t.ID == id {
			return true
		}
	}
	return false
}
