package ledger

import (
	"sort"
)

const (
	statusReady      = 3
	statusReviewable = 10
	statusLogClip    = 200
)

type StatusRow struct {
	ID         string          `json:"id"`
	Text       string          `json:"text"`
	State      WorkState       `json:"state"`
	Priority   string          `json:"priority"`
	Assignee   string          `json:"assignee,omitempty"`
	LastLog    *WorkEntry      `json:"lastLog,omitempty"`
	Handoff    *Handoff        `json:"handoff,omitempty"`
	RejectedBy string          `json:"rejectedBy,omitempty"`
	Rejection  string          `json:"rejection,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	WaitsOn    []string        `json:"waitsOn,omitempty"`
	Children   *StatusChildren `json:"children,omitempty"`
}

type StatusChildren struct {
	Unfinished []StatusChild `json:"unfinished"`
	Finished   int           `json:"finished"`
}

type StatusChild struct {
	ID       string    `json:"id"`
	Text     string    `json:"text"`
	State    WorkState `json:"state"`
	Assignee string    `json:"assignee,omitempty"`
}

// SessionStatus is one session's place in the work: what it holds and in what shape, what it handed in,
// what others handed in that it could review, and what it could take next.
type SessionStatus struct {
	SessionID       string      `json:"sessionId"`
	Holding         []StatusRow `json:"holding"`
	Rejected        []StatusRow `json:"rejected"`
	Waiting         []StatusRow `json:"waiting"`
	InReview        []StatusRow `json:"inReview"`
	Reviewable      []StatusRow `json:"reviewable"`
	ReviewableTotal int         `json:"reviewableTotal"`
	Ready           []StatusRow `json:"ready"`
}

// Rejection is who sent a held item back from review and why, until it is submitted again.
func (item WorkItem) Rejection() (by, why string, ok bool) {
	if item.State != WorkInProgress || item.Reviewer == "" {
		return "", "", false
	}
	return item.Reviewer, item.Reason, true
}

func (b *Board) SessionStatus(sessionID string) SessionStatus {
	ready := b.Ready()
	b.mu.RLock()
	defer b.mu.RUnlock()
	status := SessionStatus{SessionID: sessionID, Holding: []StatusRow{}, Rejected: []StatusRow{}, Waiting: []StatusRow{}, InReview: []StatusRow{}, Reviewable: []StatusRow{}, Ready: []StatusRow{}}
	var reviewable []*WorkItem
	for _, item := range b.workItems {
		mine := item.Assignee == sessionID
		switch {
		case mine && item.State == WorkInReview:
			status.InReview = append(status.InReview, b.statusRow(item))
		case mine && item.State == WorkBlocked:
			status.Waiting = append(status.Waiting, b.statusRow(item))
		case mine && item.State == WorkInProgress:
			row := b.statusRow(item)
			switch {
			case row.RejectedBy != "":
				status.Rejected = append(status.Rejected, row)
			case len(row.WaitsOn) > 0:
				status.Waiting = append(status.Waiting, row)
			default:
				status.Holding = append(status.Holding, row)
			}
		case !mine && item.State == WorkInReview:
			reviewable = append(reviewable, item)
		}
	}
	sort.Slice(reviewable, func(i, j int) bool {
		if pi, pj := priorityRank(reviewable[i].Priority), priorityRank(reviewable[j].Priority); pi != pj {
			return pi < pj
		}
		return idNumber(reviewable[i].ID) < idNumber(reviewable[j].ID)
	})
	status.ReviewableTotal = len(reviewable)
	for _, item := range reviewable[:min(statusReviewable, len(reviewable))] {
		status.Reviewable = append(status.Reviewable, b.statusRow(item))
	}
	for _, item := range ready[:min(statusReady, len(ready))] {
		status.Ready = append(status.Ready, b.statusRow(b.workItems[item.ID]))
	}
	for _, rows := range [][]StatusRow{status.Holding, status.Rejected, status.Waiting, status.InReview} {
		sort.Slice(rows, func(i, j int) bool { return idNumber(rows[i].ID) < idNumber(rows[j].ID) })
	}
	return status
}

func (b *Board) statusRow(item *WorkItem) StatusRow {
	row := StatusRow{ID: item.ID, Text: clipText(item.Text, readTextClip), State: item.State, Priority: item.Priority, Assignee: item.Assignee, WaitsOn: b.openDeps(item)}
	if item.State == WorkBlocked {
		row.Reason = item.Reason
	}
	if by, why, ok := item.Rejection(); ok {
		row.RejectedBy, row.Rejection = by, why
	}
	if entry := item.LatestLog(); entry != nil {
		clipped := *entry
		clipped.Text = clipText(clipped.Text, statusLogClip)
		row.LastLog = &clipped
	}
	if item.Handoff != nil {
		row.Handoff = item.Handoff.clone()
	}
	if len(item.Children) > 0 {
		children := &StatusChildren{Unfinished: []StatusChild{}}
		for _, child := range b.openChildren(item) {
			children.Unfinished = append(children.Unfinished, StatusChild{ID: child.ID, Text: clipText(child.Text, readTextClip), State: child.State, Assignee: child.Assignee})
		}
		children.Finished = len(item.Children) - len(children.Unfinished)
		row.Children = children
	}
	return row
}
