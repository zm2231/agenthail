package ledger

import (
	"fmt"
	"slices"
	"sort"
)

func (b *Board) planWorkDep(req WorkRequest) (EventType, any, string, error) {
	item, dep := b.workItems[req.ItemID], b.workItems[req.DependsOn]
	switch {
	case item == nil:
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	case dep == nil:
		return "", nil, "", refuse("%q is not a work item in %s", req.DependsOn, req.ProjectKey)
	case item.ID == dep.ID:
		return "", nil, "", refuse("%s cannot depend on itself", item.ID)
	case slices.Contains(item.DependsOn, dep.ID):
		return "", nil, "", refuse("%s already depends on %s", item.ID, dep.ID)
	case b.reaches(dep.ID, item.ID):
		return "", nil, "", refuse("%s already depends on %s, directly or through others; that would be a cycle", dep.ID, item.ID)
	}
	return EventWorkItemDep, WorkDepPayload{ItemID: item.ID, DependsOn: dep.ID, Actor: req.SessionID}, fmt.Sprintf("%s waits on %s", item.ID, dep.ID), nil
}

func (b *Board) reaches(from, to string) bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == to {
			return true
		}
		if seen[id] || b.workItems[id] == nil {
			continue
		}
		seen[id] = true
		stack = append(stack, b.workItems[id].DependsOn...)
	}
	return false
}

func (b *Board) openDeps(item *WorkItem) []string {
	var open []string
	for _, id := range item.DependsOn {
		if dep := b.workItems[id]; dep != nil && dep.State != WorkDone {
			open = append(open, id)
		}
	}
	return open
}

func (b *Board) CriticalPath() []WorkItem {
	b.mu.RLock()
	defer b.mu.RUnlock()
	memo := map[string][]string{}
	var longest func(id string) []string
	longest = func(id string) []string {
		if path, ok := memo[id]; ok {
			return path
		}
		item := b.workItems[id]
		var best []string
		deps := append([]string(nil), item.DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			if d := b.workItems[dep]; d != nil && d.State != WorkDone {
				if path := longest(dep); len(path) > len(best) {
					best = path
				}
			}
		}
		memo[id] = append(append([]string(nil), best...), id)
		return memo[id]
	}
	var best []string
	ids := make([]string, 0, len(b.workItems))
	for id, item := range b.workItems {
		if item.State != WorkDone {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return idNumber(ids[i]) < idNumber(ids[j]) })
	for _, id := range ids {
		if path := longest(id); len(path) > len(best) {
			best = path
		}
	}
	out := make([]WorkItem, 0, len(best))
	for _, id := range best {
		out = append(out, b.workItems[id].snapshot())
	}
	return out
}

func (b *Board) blockedOnOthers(c Clause, sessionID string) bool {
	waiting := false
	for _, item := range c.WorkItems {
		if item.State == WorkDone {
			continue
		}
		others := item.Assignee != "" && item.Assignee != sessionID
		for _, id := range b.openDeps(item) {
			if dep := b.workItems[id]; dep.Assignee != "" && dep.Assignee != sessionID {
				others = true
			}
		}
		if !others {
			return false
		}
		waiting = true
	}
	return waiting
}
