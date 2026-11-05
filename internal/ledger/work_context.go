package ledger

import (
	"slices"
	"sort"
)

type WorkRef struct {
	ID    string    `json:"id"`
	State WorkState `json:"state"`
	Text  string    `json:"text"`
}

type ClauseRef struct {
	ID     string      `json:"id"`
	Text   string      `json:"text"`
	State  ClauseState `json:"state"`
	Source string      `json:"source,omitempty"`
	AskID  string      `json:"askId,omitempty"`
	GoalID string      `json:"goalId,omitempty"`
	Goal   string      `json:"goal,omitempty"`
	Words  []WordsRef  `json:"words,omitempty"`
}

type WordsRef struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	At   string `json:"ts"`
}

// WorkContext is everything a session picking an item up needs in one read: the item, the intent it serves in the person's own words, where it was found, and the work around it.
type WorkContext struct {
	Item       WorkItem   `json:"item"`
	Clause     *ClauseRef `json:"clause,omitempty"`
	FoundIn    *FoundRef  `json:"foundIn,omitempty"`
	DependsOn  []WorkRef  `json:"dependsOn,omitempty"`
	Dependents []WorkRef  `json:"dependents,omitempty"`
}

type FoundRef struct {
	Work   *WorkRef   `json:"work,omitempty"`
	Clause *ClauseRef `json:"clause,omitempty"`
}

func (b *Board) WorkContext(id string) (WorkContext, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	item := b.workItems[id]
	if item == nil {
		return WorkContext{}, false
	}
	ctx := WorkContext{Item: item.snapshot(), Clause: b.clauseRef(item.ClauseID)}
	if item.From != "" {
		found := &FoundRef{Clause: b.clauseRef(item.From)}
		if from := b.workItems[item.From]; from != nil {
			found.Work = &WorkRef{ID: from.ID, State: from.State, Text: from.Text}
		}
		ctx.FoundIn = found
	}
	for _, dep := range item.DependsOn {
		if d := b.workItems[dep]; d != nil {
			ctx.DependsOn = append(ctx.DependsOn, WorkRef{ID: d.ID, State: d.State, Text: d.Text})
		}
	}
	for _, other := range b.workItems {
		if slices.Contains(other.DependsOn, id) {
			ctx.Dependents = append(ctx.Dependents, WorkRef{ID: other.ID, State: other.State, Text: other.Text})
		}
	}
	sort.Slice(ctx.Dependents, func(i, j int) bool { return idNumber(ctx.Dependents[i].ID) < idNumber(ctx.Dependents[j].ID) })
	return ctx, true
}

func (b *Board) clauseRef(id string) *ClauseRef {
	c := b.clauses[id]
	if id == "" || c == nil {
		return nil
	}
	ref := &ClauseRef{ID: c.ID, Text: c.Text, State: c.State, Source: string(c.Source), AskID: c.AskID, GoalID: c.GoalID}
	if g := b.goals[c.GoalID]; g != nil {
		ref.Goal = g.Name
	}
	for _, src := range c.Sources {
		if u := b.utterances[src]; u != nil {
			ref.Words = append(ref.Words, WordsRef{ID: u.ID, Text: u.Text, At: u.At})
		}
	}
	return ref
}
