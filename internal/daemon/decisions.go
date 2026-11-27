package daemon

import (
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
)

const (
	decisionStopped = "stopped"
	decisionHolding = "holding"
	decisionWaiting = "waiting"
)

const (
	decisionTurn   = "turn"
	decisionReview = "review"
)

var decisionGroupOrder = map[string]int{decisionStopped: 0, decisionHolding: 1, decisionWaiting: 2, decisionReview: 3}

type decisionItem struct {
	Key          string              `json:"key"`
	Group        string              `json:"group"`
	SessionID    string              `json:"sessionId"`
	ProjectID    string              `json:"projectId,omitempty"`
	Kind         string              `json:"kind"`
	Reason       string              `json:"reason"`
	Since        time.Time           `json:"since,omitzero"`
	Answerable   bool                `json:"answerable"`
	Requests     []openRequestView   `json:"requests,omitempty"`
	RequestError string              `json:"requestError,omitempty"`
	HoldsUp      []string            `json:"holdsUp,omitempty"`
	WorkItem     string              `json:"workItem,omitempty"`
	WorkTitle    string              `json:"workTitle,omitempty"`
	LedgerKey    string              `json:"ledgerKey,omitempty"`
	Phase        ledger.Phase        `json:"phase,omitempty"`
	Proof        *ledger.ProofCounts `json:"proof,omitempty"`
	PRs          []ledgerPagePR      `json:"prs,omitempty"`
}

type leadHandledItem struct {
	SessionID string    `json:"sessionId"`
	ProjectID string    `json:"projectId,omitempty"`
	HeldBy    string    `json:"heldBy"`
	Role      string    `json:"role"`
	Reason    string    `json:"reason"`
	Since     time.Time `json:"since,omitzero"`
	WorkItem  string    `json:"workItem,omitempty"`
	LedgerKey string    `json:"ledgerKey,omitempty"`
}

type decisionsPage struct {
	Decisions    []decisionItem    `json:"decisions"`
	LeadHandling []leadHandledItem `json:"leadHandling"`
}

type decisionHearer struct {
	sessionID string
	role      string
}

type decisionSources struct {
	rows      []dashboardSession
	hearer    func(dashboardSession) (decisionHearer, bool)
	requests  func([]dashboardSession) map[string]decisionRequests
	queued    map[string][]string
	parked    func([]dashboardSession) []decisionItem
	dismissed map[string]string
	lead      func(dashboardSession) (string, bool)
	workTitle func(ledgerKey, itemID string) string
	reviews   func([]dashboardSession) []reviewEntry
}

// A review goes to the project's lead first, while the lead can act, unless the lead passed it on or did the work itself.
type reviewEntry struct {
	item   decisionItem
	lead   string
	passed bool
}

func (e reviewEntry) leadHolds(rows map[string]dashboardSession) bool {
	return e.lead != "" && !e.passed && e.lead != e.item.SessionID && leadReviews(rows, e.lead)
}

// A review does not need an answer now, so an open ask or a turn that ended waiting on the person leaves the lead free to take it; only a turn stopped on a tool approval or question it cannot pass does not.
func leadReviews(rows map[string]dashboardSession, id string) bool {
	lead, live := liveRow(rows, id)
	return live && lead.WaitingOn == nil
}

type decisionRequests struct {
	requests []openRequestView
	err      error
}

func buildDecisions(sources decisionSources) decisionsPage {
	byID := make(map[string]dashboardSession, len(sources.rows))
	for _, row := range sources.rows {
		byID[row.ID] = row
	}
	page := decisionsPage{Decisions: []decisionItem{}, LeadHandling: []leadHandledItem{}}
	var asking []dashboardSession
	for _, row := range sources.rows {
		if _, _, remote := hosts.Split(row.ID); remote {
			continue
		}
		if !presentationNeedsYou(&row) {
			sources.addWaitingTurn(&page, byID, row)
			continue
		}
		if row.Attention.Kind == "request" {
			if hearer, ok := sources.hearer(row); ok && hearerHolds(byID, hearer.sessionID) {
				page.LeadHandling = append(page.LeadHandling, leadHandledItem{SessionID: row.ID, ProjectID: projectIDOf(row), HeldBy: hearer.sessionID, Role: hearer.role, Reason: row.Attention.Reason, Since: decisionSince(row)})
				continue
			}
		}
		if asksForAnswer(row) {
			asking = append(asking, row)
			continue
		}
		item := decisionItem{Key: "stopped:" + row.ID, Group: decisionStopped, SessionID: row.ID, ProjectID: projectIDOf(row), Kind: row.Attention.Kind, Reason: row.Attention.Reason, Since: decisionSince(row)}
		if !sources.dismissedNow(item) {
			page.Decisions = append(page.Decisions, item)
		}
	}
	read := map[string]decisionRequests{}
	if len(asking) > 0 {
		read = sources.requests(asking)
	}
	for _, row := range asking {
		item := decisionItem{Key: answerKey(row), SessionID: row.ID, ProjectID: projectIDOf(row), Kind: row.Attention.Kind, Reason: row.Attention.Reason, Since: decisionSince(row), HoldsUp: sources.queued[row.ID]}
		if row.Attention.Kind == AttentionAsk && row.Ask != nil && row.Ask.WorkItem != "" {
			item.WorkItem, item.LedgerKey = row.Ask.WorkItem, row.Ask.LedgerKey
			if sources.workTitle != nil {
				item.WorkTitle = sources.workTitle(item.LedgerKey, item.WorkItem)
			}
		}
		answer := read[row.ID]
		switch {
		case answer.err != nil:
			item.Key, item.Group, item.RequestError = "stopped:"+row.ID, decisionStopped, answer.err.Error()
			if sources.dismissedNow(item) {
				continue
			}
		case len(answer.requests) == 0:
			continue
		default:
			item.Answerable, item.Requests = true, answer.requests
			item.Group = decisionWaiting
			if len(item.HoldsUp) > 0 {
				item.Group = decisionHolding
			}
		}
		page.Decisions = append(page.Decisions, item)
	}
	if sources.parked != nil {
		page.Decisions = append(page.Decisions, sources.parked(sources.rows)...)
	}
	if sources.reviews != nil {
		for _, entry := range sources.reviews(sources.rows) {
			if entry.leadHolds(byID) {
				item := entry.item
				page.LeadHandling = append(page.LeadHandling, leadHandledItem{SessionID: item.SessionID, ProjectID: item.ProjectID, HeldBy: entry.lead, Role: "lead", Reason: item.Reason, Since: item.Since, WorkItem: item.WorkItem, LedgerKey: item.LedgerKey})
				continue
			}
			page.Decisions = append(page.Decisions, entry.item)
		}
	}
	sort.SliceStable(page.Decisions, func(i, j int) bool {
		a, b := page.Decisions[i], page.Decisions[j]
		if a.Group != b.Group {
			return decisionGroupOrder[a.Group] < decisionGroupOrder[b.Group]
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		return a.WorkItem < b.WorkItem
	})
	sort.SliceStable(page.LeadHandling, func(i, j int) bool {
		a, b := page.LeadHandling[i], page.LeadHandling[j]
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		if a.LedgerKey != b.LedgerKey {
			return a.LedgerKey < b.LedgerKey
		}
		return a.WorkItem < b.WorkItem
	})
	return page
}

// A wait a lead hears stays with the lead while it can act, as a lead's prompts do.
func (s decisionSources) addWaitingTurn(page *decisionsPage, rows map[string]dashboardSession, row dashboardSession) {
	end, state := row.EndTurn, row.TurnState
	if row.Archived || state == nil {
		return
	}
	if state.Escalated && state.Source != turnSourceEndTurn {
		item := decisionItem{Key: "turn:" + row.ID + ":" + state.TurnID, Group: decisionWaiting, SessionID: row.ID, ProjectID: projectIDOf(row), Kind: decisionTurn, Reason: state.Reason, Since: decisionSince(row)}
		if !s.dismissedNow(item) {
			page.Decisions = append(page.Decisions, item)
		}
		return
	}
	if !end.waits() || state.Source != turnSourceEndTurn {
		return
	}
	if state.State == turnWaitingWork {
		lead, ok := "", false
		if s.lead != nil {
			lead, ok = s.lead(row)
		}
		if !ok {
			return
		}
		if hearerHolds(rows, lead) {
			page.LeadHandling = append(page.LeadHandling, leadHandledItem{SessionID: row.ID, ProjectID: projectIDOf(row), HeldBy: lead, Role: "lead", Reason: end.WaitingOn, Since: end.EndedAt, WorkItem: end.WorkItem, LedgerKey: end.LedgerKey})
			return
		}
	} else if state.State != turnWaitingPerson {
		return
	}
	item := decisionItem{Key: "turn:" + row.ID + ":" + end.TurnID, Group: decisionWaiting, SessionID: row.ID, ProjectID: projectIDOf(row), Kind: decisionTurn, Reason: end.WaitingOn, Since: end.EndedAt, WorkItem: end.WorkItem, LedgerKey: end.LedgerKey}
	if item.WorkItem != "" && s.workTitle != nil {
		item.WorkTitle = s.workTitle(item.LedgerKey, item.WorkItem)
	}
	if !s.dismissedNow(item) {
		page.Decisions = append(page.Decisions, item)
	}
}

func answerKey(row dashboardSession) string {
	if row.Attention.Kind == AttentionAsk && row.Ask != nil {
		return "ask:" + row.Ask.ID
	}
	return "request:" + row.ID
}

func asksForAnswer(row dashboardSession) bool {
	return row.Attention.Kind == "request" || row.Attention.Kind == AttentionAsk
}

// A lead holds a prompt only while it can act and is not itself waiting on the person.
func hearerHolds(rows map[string]dashboardSession, id string) bool {
	hearer, live := liveRow(rows, id)
	if !live || hearer.TurnState != nil && hearer.TurnState.State == turnWaitingPerson {
		return false
	}
	return hearer.Attention == nil || !asksForAnswer(hearer)
}

func liveRow(rows map[string]dashboardSession, id string) (dashboardSession, bool) {
	row, known := rows[id]
	return row, known && !row.Archived && row.Display != nil && row.Display.Kind != "unavailable"
}

func decisionSince(row dashboardSession) time.Time {
	if row.WaitingOn != nil && !row.WaitingOn.Since.IsZero() {
		return row.WaitingOn.Since
	}
	if row.Ask != nil && !row.Ask.AskedAt.IsZero() {
		return row.Ask.AskedAt
	}
	return row.LastActive
}

func projectIDOf(row dashboardSession) string {
	if row.Project == nil {
		return ""
	}
	return row.Project.ID
}
