package daemon

import (
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

const (
	decisionStopped = "stopped"
	decisionHolding = "holding"
	decisionWaiting = "waiting"
)

const decisionTurn = "turn"

var decisionGroupOrder = map[string]int{decisionStopped: 0, decisionHolding: 1, decisionWaiting: 2}

type decisionItem struct {
	Key          string            `json:"key"`
	Group        string            `json:"group"`
	SessionID    string            `json:"sessionId"`
	ProjectID    string            `json:"projectId,omitempty"`
	Kind         string            `json:"kind"`
	Reason       string            `json:"reason"`
	Since        time.Time         `json:"since,omitzero"`
	Answerable   bool              `json:"answerable"`
	Requests     []openRequestView `json:"requests,omitempty"`
	RequestError string            `json:"requestError,omitempty"`
	HoldsUp      []string          `json:"holdsUp,omitempty"`
	WorkItem     string            `json:"workItem,omitempty"`
	WorkTitle    string            `json:"workTitle,omitempty"`
	LedgerKey    string            `json:"ledgerKey,omitempty"`
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
	hearer, known := rows[id]
	if !known || hearer.Archived || hearer.Display == nil || hearer.Display.Kind == "unavailable" {
		return false
	}
	if hearer.TurnState != nil && hearer.TurnState.State == turnWaitingPerson {
		return false
	}
	return hearer.Attention == nil || !asksForAnswer(hearer)
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
