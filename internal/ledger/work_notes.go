package ledger

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const maxWorkNoteRunes = 4000

var WorkLogTypes = []string{"progress", "blocker", "decision", "hypothesis", "tried", "result"}

type WorkEntry struct {
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text,omitempty"`
	State   WorkState `json:"state,omitempty"`
	LogType string    `json:"logType,omitempty"`
}

type WorkNotePayload struct {
	ItemID  string `json:"itemId"`
	Text    string `json:"text"`
	LogType string `json:"logType,omitempty"`
	Actor   string `json:"actor"`
}

func (b *Board) planWorkNote(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	text := strings.TrimSpace(req.Text)
	switch {
	case text == "":
		return "", nil, "", refuse("a %s needs its text", req.Action)
	case utf8.RuneCountInString(text) > maxWorkNoteRunes:
		return "", nil, "", refuse("a %s is at most %d characters; put the rest in a file and point to it", req.Action, maxWorkNoteRunes)
	}
	payload := WorkNotePayload{ItemID: item.ID, Text: text, Actor: req.SessionID}
	if req.Action == WorkComment {
		return EventWorkComment, payload, fmt.Sprintf("comment on %s", item.ID), nil
	}
	switch {
	case item.Assignee == "":
		return "", nil, "", refuse("%s has no assignee; the log is its assignee's running log, so use comment", item.ID)
	case item.Assignee != req.SessionID:
		return "", nil, "", refuse("%s is claimed by %s; only its assignee logs to it, so use comment", item.ID, item.Assignee)
	}
	payload.LogType = req.LogType
	if payload.LogType == "" {
		payload.LogType = WorkLogTypes[0]
	}
	if !slices.Contains(WorkLogTypes, payload.LogType) {
		return "", nil, "", refuse("unknown log type %q; use %s", payload.LogType, strings.Join(WorkLogTypes, ", "))
	}
	return EventWorkLog, payload, fmt.Sprintf("%s log on %s", payload.LogType, item.ID), nil
}

func (b *Board) applyWorkNote(event Event) error {
	var p WorkNotePayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item := b.workItems[p.ItemID]
	if item == nil {
		return nil
	}
	kind := "comment"
	if event.Type == EventWorkLog {
		kind = "log"
	}
	item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: kind, Text: p.Text, LogType: p.LogType})
	return nil
}

func (b *Board) WorkItem(id string) (WorkItem, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	item := b.workItem(id)
	if item == nil {
		return WorkItem{}, false
	}
	return item.snapshot(), true
}

func (item *WorkItem) snapshot() WorkItem {
	out := *item
	out.DependsOn = slices.Clone(item.DependsOn)
	out.Labels = slices.Clone(item.Labels)
	out.Timeline = slices.Clone(item.Timeline)
	if item.Handoff != nil {
		h := *item.Handoff
		h.Done, h.Remaining = slices.Clone(h.Done), slices.Clone(h.Remaining)
		h.Decisions, h.Uncertain = slices.Clone(h.Decisions), slices.Clone(h.Uncertain)
		out.Handoff = &h
	}
	if item.Park != nil {
		park := item.Park.clone()
		out.Park = &park
	}
	return out
}

func (b *Board) Ready() []WorkItem {
	b.mu.RLock()
	defer b.mu.RUnlock()
	unblocks := map[string]int{}
	for id, item := range b.workItems {
		if item.State.settled() {
			continue
		}
		for other := range b.workItems {
			if other != id && b.reaches(id, other) {
				unblocks[other]++
			}
		}
	}
	var ready []WorkItem
	for _, item := range b.workItems {
		if item.State == WorkOpen && len(b.openDeps(item)) == 0 {
			ready = append(ready, item.snapshot())
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if pi, pj := priorityRank(ready[i].Priority), priorityRank(ready[j].Priority); pi != pj {
			return pi < pj
		}
		if unblocks[ready[i].ID] != unblocks[ready[j].ID] {
			return unblocks[ready[i].ID] > unblocks[ready[j].ID]
		}
		return idNumber(ready[i].ID) < idNumber(ready[j].ID)
	})
	return ready
}
