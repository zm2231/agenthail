package ledger

import "strings"

func (p *attachPlan) idea() error {
	if p.req.To != "" {
		return refuse("an idea is found work under no goal; drop --to %s, and link it to a clause once the person takes it up", p.req.To)
	}
	title, err := p.singleText("the idea's title")
	if err != nil {
		return err
	}
	for _, item := range p.board.workItems {
		if item.Park != nil && item.Park.Idea != nil && item.Park.Idea.UtteranceID == p.utterance.ID {
			return refuse("%s already keeps %s as an idea", item.ID, p.utterance.ID)
		}
	}
	id, err := p.ids.next(EventWorkItem, "W")
	if err != nil {
		return err
	}
	shape := ParkIdeaShape{UtteranceID: p.utterance.ID}
	if s := p.req.Idea; s != nil {
		shape.Goal, shape.Plan, shape.FirstTest = strings.TrimSpace(s.Goal), strings.TrimSpace(s.Plan), strings.TrimSpace(s.FirstTest)
	}
	for _, field := range []struct{ name, text string }{{"goal", shape.Goal}, {"plan", shape.Plan}, {"first test", shape.FirstTest}} {
		if err := fieldLength(field.name, field.text); err != nil {
			return err
		}
	}
	if err := p.add(EventWorkItem, WorkItemPayload{ItemID: id, Text: title, CreatedBy: p.req.SessionID, WorkFields: WorkFields{Type: WorkIdea}}); err != nil {
		return err
	}
	park := &Park{Kind: ParkIdea, Idea: &shape, Session: p.req.SessionID}
	if err := p.add(EventWorkItemState, WorkItemStatePayload{ItemID: id, FromState: WorkOpen, ToState: WorkParked, Actor: p.req.SessionID, Park: park}); err != nil {
		return err
	}
	p.effects = append(p.effects, id)
	p.summary = "+" + id + " idea, parked (" + park.Summary() + ")"
	return nil
}
