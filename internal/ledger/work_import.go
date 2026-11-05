package ledger

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ImportedFrom names the item in the tracker it came from ("td:td-1a2b3c"); importing the same name again is a no-op.
type WorkImport struct {
	ImportedFrom string      `json:"importedFrom"`
	Text         string      `json:"text"`
	Fields       WorkFields  `json:"fields"`
	State        WorkState   `json:"state"`
	Reason       string      `json:"reason,omitempty"`
	Reviewer     string      `json:"reviewer,omitempty"`
	Handoff      *Handoff    `json:"handoff,omitempty"`
	CreatedAt    time.Time   `json:"createdAt"`
	CreatedBy    string      `json:"createdBy,omitempty"`
	Timeline     []WorkEntry `json:"timeline,omitempty"`
}

type WorkImportDep struct {
	Item string `json:"item"`
	On   string `json:"on"`
}

type WorkImportRequest struct {
	ProjectKey string          `json:"projectKey"`
	SessionID  string          `json:"sessionId"`
	Items      []WorkImport    `json:"items,omitempty"`
	Deps       []WorkImportDep `json:"deps,omitempty"`
}

type WorkImported struct {
	ImportedFrom string `json:"importedFrom"`
	ItemID       string `json:"itemId"`
}

type WorkImportSkip struct {
	ImportedFrom string `json:"importedFrom"`
	Reason       string `json:"reason"`
}

type WorkImportResult struct {
	Imported []WorkImported   `json:"imported,omitempty"`
	Present  []WorkImported   `json:"present,omitempty"`
	Deps     int              `json:"deps"`
	Skipped  []WorkImportSkip `json:"skipped,omitempty"`
}

type WorkHistory struct {
	State     WorkState   `json:"state"`
	Reason    string      `json:"reason,omitempty"`
	Reviewer  string      `json:"reviewer,omitempty"`
	Handoff   *Handoff    `json:"handoff,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	Timeline  []WorkEntry `json:"timeline,omitempty"`
}

var importableStates = []WorkState{WorkOpen, WorkBlocked, WorkDone, WorkClosed}

func (s *Store) ImportWork(req WorkImportRequest, now time.Time) (WorkImportResult, error) {
	var result WorkImportResult
	if req.ProjectKey == "" || req.SessionID == "" {
		return result, refuse("project and acting session are required")
	}
	err := s.writeEvents(req.ProjectKey, req.SessionID, now, func(board *Board, ids *idIssuer) ([]plannedWrite, error) {
		result = WorkImportResult{}
		return board.planImport(req, ids, now, &result)
	})
	return result, err
}

func (b *Board) planImport(req WorkImportRequest, ids *idIssuer, now time.Time, result *WorkImportResult) ([]plannedWrite, error) {
	byOrigin := map[string]string{}
	for id, item := range b.workItems {
		if item.ImportedFrom != "" {
			byOrigin[item.ImportedFrom] = id
		}
	}
	var events []plannedWrite
	for _, in := range req.Items {
		origin := strings.TrimSpace(in.ImportedFrom)
		if origin == "" {
			return nil, refuse("every imported item needs importedFrom, the name it had in the tracker it came from")
		}
		if id, ok := byOrigin[origin]; ok {
			result.Present = append(result.Present, WorkImported{ImportedFrom: origin, ItemID: id})
			continue
		}
		payload, err := importPayload(in, origin, req.SessionID)
		if err != nil {
			result.Skipped = append(result.Skipped, WorkImportSkip{ImportedFrom: origin, Reason: err.Error()})
			continue
		}
		if payload.ItemID, err = ids.next(EventWorkItem, "W"); err != nil {
			return nil, err
		}
		byOrigin[origin] = payload.ItemID
		events = append(events, plannedWrite{EventWorkItem, payload})
		result.Imported = append(result.Imported, WorkImported{ImportedFrom: origin, ItemID: payload.ItemID})
	}
	added := map[string][]string{}
	waitsOn := func(id string) []string {
		var deps []string
		if item := b.workItems[id]; item != nil {
			deps = item.DependsOn
		}
		return append(append([]string(nil), deps...), added[id]...)
	}
	for _, dep := range req.Deps {
		item, on := byOrigin[dep.Item], byOrigin[dep.On]
		label := dep.Item + " waits on " + dep.On
		switch {
		case item == "" || on == "":
			result.Skipped = append(result.Skipped, WorkImportSkip{ImportedFrom: label, Reason: "one side was not imported"})
			continue
		case item == on:
			result.Skipped = append(result.Skipped, WorkImportSkip{ImportedFrom: label, Reason: "an item cannot wait on itself"})
			continue
		case slices.Contains(waitsOn(item), on):
			continue
		case reachesThrough(waitsOn, on, item):
			result.Skipped = append(result.Skipped, WorkImportSkip{ImportedFrom: label, Reason: fmt.Sprintf("%s already waits on %s, so this would be a cycle", on, item)})
			continue
		}
		added[item] = append(added[item], on)
		events = append(events, plannedWrite{EventWorkItemDep, WorkDepPayload{ItemID: item, DependsOn: on, Actor: req.SessionID}})
		result.Deps++
	}
	return events, nil
}

func importPayload(in WorkImport, origin, session string) (WorkItemPayload, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return WorkItemPayload{}, refuse("it has no text")
	}
	fields, err := in.Fields.normalized()
	if err != nil {
		return WorkItemPayload{}, err
	}
	if fields.From != "" {
		return WorkItemPayload{}, refuse("an imported item is found work with no --found-in")
	}
	state := in.State
	if state == "" {
		state = WorkOpen
	}
	if !slices.Contains(importableStates, state) {
		return WorkItemPayload{}, refuse("an imported item is %s, not %s", joinStates(importableStates), state)
	}
	createdBy := in.CreatedBy
	if createdBy == "" {
		createdBy = session
	}
	return WorkItemPayload{Text: text, CreatedBy: createdBy, WorkFields: fields, ImportedFrom: origin,
		History: &WorkHistory{State: state, Reason: strings.TrimSpace(in.Reason), Reviewer: in.Reviewer, Handoff: in.Handoff, CreatedAt: in.CreatedAt, Timeline: in.Timeline}}, nil
}

func reachesThrough(waitsOn func(string) []string, from, to string) bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == to {
			return true
		}
		if !seen[id] {
			seen[id] = true
			stack = append(stack, waitsOn(id)...)
		}
	}
	return false
}
