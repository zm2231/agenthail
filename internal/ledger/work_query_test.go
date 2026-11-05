package ledger_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func queried(t *testing.T, b *ledger.Board, q ledger.WorkQuery) string {
	t.Helper()
	items, err := b.QueryWork(q)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = strings.TrimPrefix(item.ID, "widget-")
	}
	return strings.Join(ids, " ")
}

func TestWorkQueriesFindItemsByWordsFieldsAndNotes(t *testing.T) {
	s := newStore(t)
	add := func(text string, f ledger.WorkFields) string {
		return work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, Text: text, Fields: f}).Item.ID
	}
	leak := add("index build leaks a file handle", ledger.WorkFields{Type: ledger.WorkBug, Priority: "P1", Labels: []string{"index", "perf"}})
	add("rename the search flag", ledger.WorkFields{Priority: "P3", Labels: []string{"index"}, Description: "users type --find"})
	slow := add("slow startup", ledger.WorkFields{Type: ledger.WorkBug, Priority: "P0", Labels: []string{"perf"}})
	gone := add("old idea about the index", ledger.WorkFields{})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: slow, Text: "the Handle cache is cold"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: leak, Reason: "duplicate of W9"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDelete, ItemID: gone})
	if _, err := s.ImportWork(ledger.WorkImportRequest{ProjectKey: project, SessionID: "s1", Items: []ledger.WorkImport{
		{ImportedFrom: "td:td-50b9b1", Text: "carried over from td", CreatedAt: time.Now().Add(-time.Hour)}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	b := board(t, s)

	for _, c := range []struct {
		name string
		q    ledger.WorkQuery
		want string
	}{
		{"every word, in any field or note, in any state but deleted", ledger.WorkQuery{Terms: []string{"HANDLE"}, All: true}, "W1 W3"},
		{"words narrow each other", ledger.WorkQuery{Terms: []string{"handle", "cache"}, All: true}, "W3"},
		{"description and td id are searched", ledger.WorkQuery{Terms: []string{"--find"}, All: true}, "W2"},
		{"the td id", ledger.WorkQuery{Terms: []string{"td-50b9b1"}, All: true}, "W5"},
		{"deleted only by state", ledger.WorkQuery{Terms: []string{"idea"}, All: true}, ""},
		{"deleted by state", ledger.WorkQuery{States: []ledger.WorkState{ledger.WorkDeleted}}, "W4"},
		{"unsettled by default", ledger.WorkQuery{}, "W2 W3 W5"},
		{"every label must match", ledger.WorkQuery{Labels: []string{"Perf", "index"}, All: true}, "W1"},
		{"priorities as a list", ledger.WorkQuery{Priorities: []string{"0", "P3"}}, "W2 W3"},
		{"types", ledger.WorkQuery{Types: []ledger.WorkType{"BUG"}, All: true}, "W1 W3"},
		{"several states", ledger.WorkQuery{States: []ledger.WorkState{ledger.WorkClosed, ledger.WorkOpen}, Sort: "priority"}, "W3 W1 W5 W2"},
		{"most urgent first, then a limit", ledger.WorkQuery{All: true, Sort: "priority", Limit: 2}, "W3 W1"},
	} {
		if got := queried(t, b, c.q); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	for _, bad := range []ledger.WorkQuery{{Priorities: []string{"P9"}}, {Sort: "random"}, {States: []ledger.WorkState{"finished"}}, {Limit: -1}} {
		if _, err := b.QueryWork(bad); err == nil {
			t.Errorf("query %+v was accepted", bad)
		}
	}
	for _, name := range []string{"td-50b9b1", "td:td-50b9b1"} {
		if item, ok := b.WorkItemByOrigin(name); !ok || item.ID != "widget-W5" {
			t.Errorf("by origin %s: %+v %v", name, item, ok)
		}
	}
	if _, ok := b.WorkItemByOrigin("td-5"); ok {
		t.Error("a prefix of a td id found an item")
	}
}

func TestWorkQueriesSortByWhenItemsWereCreatedAndLastTouched(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	var items []ledger.WorkImport
	for i, text := range []string{"oldest", "middle", "newest"} {
		items = append(items, ledger.WorkImport{ImportedFrom: "td:td-" + text, Text: text, CreatedAt: t0.Add(time.Duration(i) * time.Hour)})
	}
	if _, err := s.ImportWork(ledger.WorkImportRequest{ProjectKey: project, SessionID: "s1", Items: items}, t0.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := queried(t, board(t, s), ledger.WorkQuery{Sort: "created"}); got != "W3 W2 W1" {
		t.Fatalf("newest created first: %q", got)
	}
	comment := ledger.WorkRequest{ProjectKey: project, SessionID: "s1", Action: ledger.WorkComment, ItemID: "widget-W1", Text: "still happens"}
	if _, err := s.Work(comment, t0.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := queried(t, board(t, s), ledger.WorkQuery{Sort: "updated"}); got != "W1 W2 W3" {
		t.Fatalf("most recently touched first: %q", got)
	}
}
