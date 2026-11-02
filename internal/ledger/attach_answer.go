package ledger

import (
	"fmt"
	"strings"
)

func (p *attachPlan) answer() error {
	q := p.board.questions[p.req.To]
	if q == nil {
		return refuse("--as answers needs --to <question>; %q is not a question in %s", p.req.To, p.req.ProjectKey)
	}
	if q.State != QuestionOpen {
		return refuse("%s is already %s", q.ID, q.State)
	}
	if len(p.req.Clauses) > 1 {
		return refuse("--as answers takes at most one --clause: what the answer changes")
	}
	p.goalID = q.GoalID
	resolved := QuestionResolvedPayload{QuestionID: q.ID, Answer: p.utterance.Text, SourceUtteranceID: p.utterance.ID}
	p.summary = q.ID + " answered"
	if len(p.req.Clauses) == 1 {
		text := strings.TrimSpace(p.req.Clauses[0])
		if text == "" {
			return refuse("a --clause is empty")
		}
		effect, err := p.applyAnswer(q, text)
		if err != nil {
			return err
		}
		resolved.Applied, resolved.Effects = true, []string{effect}
		p.summary += fmt.Sprintf(" (%s)", p.answerEffect(q, effect))
	}
	p.effects = append(p.effects, q.ID)
	p.effects = append(p.effects, resolved.Effects...)
	return p.add(EventQuestionRes, resolved)
}

func (p *attachPlan) applyAnswer(q *Question, text string) (string, error) {
	if c := p.board.clauses[q.Target]; c != nil {
		if c.State == StateRejected {
			return "", refuse("%s was rejected", c.ID)
		}
		return c.ID, p.add(EventSupersession, SupersessionPayload{ClauseID: c.ID, OldVersion: c.Text, NewVersion: text, SourceUtteranceID: p.utterance.ID})
	}
	if ask := p.board.asks[q.Target]; ask != nil {
		id, err := p.ids.next(EventClause, "C")
		if err != nil {
			return "", err
		}
		return id, p.add(EventClause, ClausePayload{ClauseID: id, AskID: ask.ID, GoalID: ask.GoalID, Text: text, State: StateCaptured,
			Source: SourceStated, OwnerSessionID: p.utterance.TargetID, SourceUtteranceID: p.utterance.ID})
	}
	id, err := p.ids.next(EventDecision, "D")
	if err != nil {
		return "", err
	}
	return id, p.add(EventDecision, DecisionPayload{DecisionID: id, GoalID: q.GoalID, QuestionID: q.ID, Version: 1, Text: text,
		Principal: p.utterance.SpeakerID, SourceUtterance: p.utterance.ID})
}

func (p *attachPlan) answerEffect(q *Question, effect string) string {
	switch {
	case p.board.clauses[q.Target] != nil:
		return effect + " refined"
	case p.board.asks[q.Target] != nil:
		return fmt.Sprintf("+%s on %s", effect, q.Target)
	}
	return fmt.Sprintf("+%s decided on %s", effect, p.label(q.GoalID))
}
