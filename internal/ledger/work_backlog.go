package ledger

import (
	"fmt"
	"slices"
	"sort"
	"time"
)

type Headroom struct {
	Known bool
	Room  bool
	Frees time.Time
}

type UsageReader func(ParkUsageTarget) Headroom

type AskReader func(askID string) (answered bool)

type Readings struct {
	Usage UsageReader
	Asks  AskReader
}

type BacklogQuery struct {
	Kinds []ParkKind
	Ready bool
}

type BacklogEntry struct {
	Item    WorkItem  `json:"item"`
	Ready   bool      `json:"ready"`
	Why     string    `json:"why"`
	ReadyAt time.Time `json:"readyAt,omitzero"`
}

func (b *Board) Backlog(q BacklogQuery, now time.Time, readings Readings) []BacklogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entries := []BacklogEntry{}
	for _, item := range b.workItems {
		if item.State != WorkParked || item.Park == nil || (len(q.Kinds) > 0 && !slices.Contains(q.Kinds, item.Park.Kind)) {
			continue
		}
		entry := BacklogEntry{Item: item.snapshot()}
		entry.Ready, entry.Why, entry.ReadyAt = parkReadiness(*item.Park, now, readings)
		if waits := b.openDeps(item); entry.Ready && len(waits) > 0 {
			entry.Ready, entry.Why = false, "waits on "+waits[0]
		}
		if q.Ready && !entry.Ready {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Ready != entries[j].Ready {
			return entries[i].Ready
		}
		return CompareIDs(entries[i].Item.ID, entries[j].Item.ID) < 0
	})
	return entries
}

func parkReadiness(park Park, now time.Time, readings Readings) (bool, string, time.Time) {
	switch park.Kind {
	case ParkIdea:
		return false, "an idea waits for someone to take it up", time.Time{}
	case ParkPerson:
		if park.Person.AskID != "" && readings.Asks != nil && readings.Asks(park.Person.AskID) {
			return true, "the person answered " + park.Person.AskID, time.Time{}
		}
		return false, "waits on the person's answer", time.Time{}
	case ParkLater:
		if park.Revisit.IsZero() {
			return false, "no revisit date", time.Time{}
		}
		if now.Before(park.Revisit) {
			return false, "revisit " + park.Revisit.UTC().Format("2006-01-02"), park.Revisit
		}
		return true, "its revisit date came", park.Revisit
	case ParkUsage:
		return usageReadiness(*park.Usage, now, readings.Usage)
	}
	return false, fmt.Sprintf("unknown park kind %q", park.Kind), time.Time{}
}

func usageReadiness(target ParkUsageTarget, now time.Time, usage UsageReader) (bool, string, time.Time) {
	name := target.Provider
	if target.Root != "" {
		name += ":" + target.Root
	}
	if !target.ResetsAt.IsZero() && !now.Before(target.ResetsAt) {
		return true, name + " reset at " + target.ResetsAt.UTC().Format(time.RFC3339), target.ResetsAt
	}
	var room Headroom
	if usage != nil {
		room = usage(target)
	}
	switch {
	case room.Known && room.Room:
		return true, name + " has headroom", time.Time{}
	case room.Known:
		frees := room.Frees
		if frees.IsZero() {
			frees = target.ResetsAt
		}
		if frees.IsZero() {
			return false, name + " is at its limit", time.Time{}
		}
		return false, name + " is at its limit until " + frees.UTC().Format(time.RFC3339), frees
	case !target.ResetsAt.IsZero():
		return false, name + " resets at " + target.ResetsAt.UTC().Format(time.RFC3339), target.ResetsAt
	}
	return false, "no usage reading for " + name, time.Time{}
}

func (b *Board) Triage(now time.Time, idle time.Duration) []WorkItem {
	b.mu.RLock()
	defer b.mu.RUnlock()
	items := []WorkItem{}
	for _, item := range b.workItems {
		if (item.State == WorkOpen || item.State == WorkBlocked) && item.Assignee == "" && !item.updated().After(now.Add(-idle)) {
			items = append(items, item.snapshot())
		}
	}
	sort.Slice(items, func(i, j int) bool { return CompareIDs(items[i].ID, items[j].ID) < 0 })
	return items
}
