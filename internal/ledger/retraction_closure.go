package ledger

import "strings"

func dependencies(t EventType, src eventSource) []string {
	var keys []string
	add := func(kind, id string) {
		if id != "" {
			keys = append(keys, skippedKey(kind, id))
		}
	}
	if t != EventAsk {
		add(RetractedAsk, src.AskID)
	}
	if t != EventClause {
		add(RetractedClause, src.ClauseID)
	}
	if t != EventDecision {
		add(RetractedDecision, src.DecisionID)
	}
	if t != EventQuestion {
		add(RetractedQuestion, src.QuestionID)
	}
	if t != EventWorkItem {
		add(RetractedWorkItem, src.ItemID)
	}
	if t != EventCut {
		add(RetractedCut, src.CutID)
	}
	add(RetractedWorkItem, src.DependsOn)
	add(RetractedAsk, src.Target)
	add(RetractedClause, src.Target)
	return keys
}

// orphan drops an event that rests on something dropped as retracted, and anything it creates with it.
func (b *Board) orphan(t EventType, src eventSource) bool {
	for _, dep := range dependencies(t, src) {
		root, ok := b.dropped[dep]
		if !ok {
			root, ok = b.orphaned[dep]
		}
		if !ok {
			continue
		}
		if key := createdKey(t, src); key != "" {
			b.orphaned[key] = root
			kind, id, _ := strings.Cut(key, ":")
			b.retractions = append(b.retractions, Retraction{Kind: kind, ID: id, UtteranceID: root, Reason: b.retracted[root], DependsOn: dep})
		}
		return true
	}
	return false
}
