package ledger

import (
	"encoding/json"
	"time"
)

type QuestionState string

const (
	QuestionOpen      QuestionState = "open"
	QuestionAnswered  QuestionState = "answered"
	QuestionDefaulted QuestionState = "defaulted"
)

type Question struct {
	ID             string        `json:"id"`
	Target         string        `json:"target"`
	GoalID         string        `json:"goalId,omitempty"`
	Kind           QuestionKind  `json:"kind"`
	Text           string        `json:"text"`
	Recommendation string        `json:"recommendation"`
	DefaultAnswer  string        `json:"defaultAnswer"`
	DeadlineMs     int64         `json:"deadlineMs"`
	Narrows        bool          `json:"narrows,omitempty"`
	OwnerSessionID string        `json:"ownerSessionId"`
	State          QuestionState `json:"state"`
	Escalated      bool          `json:"escalated,omitempty"`
	Answer         string        `json:"answer,omitempty"`
	AnsweredBy     string        `json:"answeredBy,omitempty"`
	Applied        bool          `json:"applied,omitempty"`
}

// AutoApplies is the never-resolve-toward-less rule: only a scope or framing default that keeps breadth applies itself.
func (q Question) AutoApplies() bool {
	return q.Kind != QuestionOutward && !q.Narrows
}

func (q Question) Expired(now time.Time) bool {
	return q.State == QuestionOpen && now.UnixMilli() >= q.DeadlineMs
}

type TargetCut struct {
	Cut
	Target string `json:"target"`
}

func (b *Board) applyQuestion(data []byte) error {
	var p QuestionPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	b.questions[p.QuestionID] = &Question{
		ID: p.QuestionID, Target: p.Target, GoalID: p.GoalID, Kind: p.Kind, Text: p.Text,
		Recommendation: p.Recommendation, DefaultAnswer: p.DefaultAnswer, DeadlineMs: p.DeadlineMs,
		Narrows: p.Narrows, OwnerSessionID: p.OwnerSessionID, State: QuestionOpen,
	}
	return nil
}

func (b *Board) applyQuestionResolved(data []byte) error {
	var p QuestionResolvedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	q := b.questions[p.QuestionID]
	if q == nil {
		return nil
	}
	q.State, q.Answer, q.AnsweredBy, q.Applied = QuestionAnswered, p.Answer, p.SourceUtteranceID, p.Applied
	if p.Auto {
		q.State = QuestionDefaulted
	}
	b.withdrawQuestionCuts(q.ID)
	return nil
}

func (b *Board) withdrawQuestionCuts(questionID string) {
	withdraw := func(c *Cut) {
		if c.QuestionID == questionID && !c.Acked {
			c.Withdrawn = true
		}
	}
	for _, c := range b.clauses {
		for i := range c.Cuts {
			withdraw(&c.Cuts[i])
		}
	}
	for _, t := range b.targetCuts {
		withdraw(&t.Cut)
	}
}

func (b *Board) applyQuestionEscalated(data []byte) error {
	var p QuestionEscalatedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if q := b.questions[p.QuestionID]; q != nil {
		q.Escalated = true
	}
	return nil
}

func (b *Board) Questions() []Question {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Question, 0, len(b.questions))
	for _, q := range b.questions {
		out = append(out, *q)
	}
	return out
}

func (b *Board) TargetCuts() []TargetCut {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]TargetCut, 0, len(b.targetCuts))
	for _, t := range b.targetCuts {
		out = append(out, *t)
	}
	return out
}
