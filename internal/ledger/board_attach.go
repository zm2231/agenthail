package ledger

import (
	"encoding/json"
	"slices"
	"strings"
)

func (b *Board) applyUtterance(data []byte) error {
	var p UtterancePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.UtteranceID == "" || b.utterances[p.UtteranceID] != nil {
		return nil
	}
	b.utterances[p.UtteranceID] = &UtteranceView{ID: p.UtteranceID, Text: p.Text, SpeakerID: p.SessionID, TargetID: p.TargetID, CapturedBy: p.CapturedBy, At: p.TS}
	b.utteranceOrder = append(b.utteranceOrder, p.UtteranceID)
	return nil
}

func (b *Board) applyAttach(data []byte) error {
	var p AttachPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	u := b.utterances[p.UtteranceID]
	if u == nil {
		return nil
	}
	if !slices.Contains(b.attachedBy[p.UtteranceID], p.SessionID) {
		b.attachedBy[p.UtteranceID] = append(b.attachedBy[p.UtteranceID], p.SessionID)
	}
	u.Attached = true
	if p.GoalID != "" && !slices.Contains(u.GoalIDs, p.GoalID) {
		u.GoalIDs = append(u.GoalIDs, p.GoalID)
	}
	return nil
}

func (b *Board) applyClauseSource(data []byte) error {
	var p ClauseSourcePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c, ok := b.clauses[p.ClauseID]
	if !ok {
		return nil
	}
	c.Source = p.ToSource
	if p.OwnerSessionID != "" {
		c.OwnerSessionID = p.OwnerSessionID
	}
	if p.UtteranceID != "" {
		c.Sources = append(c.Sources, p.UtteranceID)
	}
	return nil
}

func (b *Board) goalByName(name string) *Goal {
	for _, g := range b.goals {
		if strings.EqualFold(g.Name, name) {
			return g
		}
	}
	return nil
}

func (b *Board) clauseGoal(c *Clause) string {
	if ask := b.asks[c.AskID]; ask != nil {
		return ask.GoalID
	}
	return c.GoalID
}
