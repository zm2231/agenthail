package ledger

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// WorkQuery selects work items. Without States it keeps unsettled items, or with All every item but deleted ones;
// Terms must each appear, case-insensitively, in the item's id, origin, text, fields, labels or notes.
type WorkQuery struct {
	Terms      []string    `json:"terms,omitempty"`
	States     []WorkState `json:"states,omitempty"`
	All        bool        `json:"all,omitempty"`
	Types      []WorkType  `json:"types,omitempty"`
	Priorities []string    `json:"priorities,omitempty"`
	Labels     []string    `json:"labels,omitempty"`
	Assignee   string      `json:"assignee,omitempty"`
	Clause     string      `json:"clause,omitempty"`
	Found      bool        `json:"found,omitempty"`
	Sort       string      `json:"sort,omitempty"`
	Limit      int         `json:"limit,omitempty"`
}

var workSorts = []string{"id", "priority", "created", "updated"}

func (q WorkQuery) normalized() (WorkQuery, error) {
	q.Priorities, q.Types, q.Terms = slices.Clone(q.Priorities), slices.Clone(q.Types), slices.Clone(q.Terms)
	for i, p := range q.Priorities {
		norm, err := workPriority(p)
		if err != nil {
			return q, err
		}
		q.Priorities[i] = norm
	}
	for i, t := range q.Types {
		norm, err := workType(string(t))
		if err != nil {
			return q, err
		}
		q.Types[i] = norm
	}
	for _, s := range q.States {
		if !slices.Contains(allWorkStates, s) {
			return q, refuse("unknown state %q; use %s", s, joinStates(allWorkStates))
		}
	}
	q.Labels = normalLabels(q.Labels)
	for i, t := range q.Terms {
		q.Terms[i] = strings.ToLower(t)
	}
	if q.Sort == "" {
		q.Sort = "id"
	}
	if !slices.Contains(workSorts, q.Sort) {
		return q, refuse("unknown sort %q; use %s", q.Sort, strings.Join(workSorts, ", "))
	}
	if q.Limit < 0 {
		return q, refuse("--limit is a number of items, 0 for all")
	}
	return q, nil
}

var allWorkStates = []WorkState{WorkOpen, WorkInProgress, WorkBlocked, WorkInReview, WorkDone, WorkClosed, WorkDeleted}

func (q WorkQuery) keeps(item *WorkItem) bool {
	switch {
	case len(q.Types) > 0 && !slices.Contains(q.Types, item.Type),
		len(q.Priorities) > 0 && !slices.Contains(q.Priorities, item.Priority),
		q.Assignee != "" && item.Assignee != q.Assignee,
		q.Clause != "" && item.ClauseID != q.Clause,
		q.Found && item.ClauseID != "":
		return false
	}
	for _, l := range q.Labels {
		if !slices.Contains(item.Labels, l) {
			return false
		}
	}
	switch {
	case len(q.States) > 0:
		if !slices.Contains(q.States, item.State) {
			return false
		}
	case item.State == WorkDeleted:
		return false
	case !q.All && item.State.settled():
		return false
	}
	if len(q.Terms) == 0 {
		return true
	}
	text := item.searchText()
	for _, t := range q.Terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return true
}

func (item *WorkItem) searchText() string {
	parts := []string{item.ID, item.ImportedFrom, item.Text, item.Description, item.Acceptance, item.From, item.Reason, strings.Join(item.Labels, " ")}
	for _, e := range item.Timeline {
		parts = append(parts, e.Text)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

func (item *WorkItem) updated() time.Time {
	if n := len(item.Timeline); n > 0 {
		return item.Timeline[n-1].At
	}
	return time.Time{}
}

func (item *WorkItem) created() time.Time {
	if len(item.Timeline) > 0 {
		return item.Timeline[0].At
	}
	return time.Time{}
}

func (b *Board) QueryWork(q WorkQuery) ([]WorkItem, error) {
	q, err := q.normalized()
	if err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	var kept []*WorkItem
	for _, item := range b.workItems {
		if q.keeps(item) {
			kept = append(kept, item)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		a, c := kept[i], kept[j]
		switch q.Sort {
		case "priority":
			if pa, pc := priorityRank(a.Priority), priorityRank(c.Priority); pa != pc {
				return pa < pc
			}
		case "created":
			if !a.created().Equal(c.created()) {
				return a.created().After(c.created())
			}
		case "updated":
			if !a.updated().Equal(c.updated()) {
				return a.updated().After(c.updated())
			}
		}
		return idNumber(a.ID) < idNumber(c.ID)
	})
	if q.Limit > 0 && len(kept) > q.Limit {
		kept = kept[:q.Limit]
	}
	out := make([]WorkItem, len(kept))
	for i, item := range kept {
		out[i] = item.snapshot()
	}
	return out, nil
}

// workItem finds an item by its id or, for an imported item, by the whole id it had where it came from, with or without the source prefix.
func (b *Board) workItem(ref string) *WorkItem {
	if item := b.workItems[ref]; item != nil || ref == "" {
		return item
	}
	for _, item := range b.workItems {
		if o := item.ImportedFrom; o != "" && (o == ref || strings.HasSuffix(o, ":"+ref)) {
			return item
		}
	}
	return nil
}

func (b *Board) canonicalWorkRefs(req WorkRequest) WorkRequest {
	for _, ref := range []*string{&req.ItemID, &req.DependsOn, &req.Fields.From} {
		if item := b.workItem(*ref); item != nil {
			*ref = item.ID
		}
	}
	return req
}
