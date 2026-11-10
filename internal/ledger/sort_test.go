package ledger_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func sortTo(t *testing.T, s *ledger.Store, ref, goalID string, confidence float64) bool {
	t.Helper()
	filed, err := s.RecordSort(project, ledger.SortedPayload{UtteranceID: ref, GoalID: goalID, Confidence: confidence, Model: "d1-3b", Ms: 700}, time.Now())
	if err != nil {
		t.Fatalf("sort %s to %s: %v", ref, goalID, err)
	}
	return filed
}

func utteranceView(t *testing.T, s *ledger.Store, ref string) ledger.UtteranceView {
	t.Helper()
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range board.Utterances() {
		if u.ID == ref {
			return u
		}
	}
	t.Fatalf("no utterance %s", ref)
	return ledger.UtteranceView{}
}

func provisionalIDs(t *testing.T, s *ledger.Store) []string {
	t.Helper()
	state, err := s.ProvisionalFor(project, "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, u := range state {
		ids = append(ids, u.ID)
	}
	return ids
}

func TestASortFilesAProvisionalMessageUnderAnExistingGoalOnlyWhenConfident(t *testing.T) {
	s := newStore(t)
	search := goal(t, s, "search")
	capture(t, s, project, "claude-prompt:1", "the search box should rank exact matches first")
	capture(t, s, project, "claude-prompt:2", "and make it typo tolerant")
	if _, err := s.RecordSort(project, ledger.SortedPayload{UtteranceID: "claude-prompt:1", GoalID: search, Confidence: 0.89}, time.Now()); err == nil {
		t.Fatal("a sort below 0.9 was accepted")
	}
	if sortTo(t, s, "claude-prompt:1", "widget-G99", 0.95) {
		t.Fatal("a sort to a goal that does not exist was filed")
	}
	if !sortTo(t, s, "claude-prompt:1", search, 0.93) {
		t.Fatal("a confident sort to an existing goal was not filed")
	}
	if sortTo(t, s, "claude-prompt:1", search, 0.97) {
		t.Fatal("a message was sorted twice")
	}
	u := utteranceView(t, s, "claude-prompt:1")
	if u.Sorted == nil || u.Sorted.GoalID != search || u.Sorted.Confidence != 0.93 || u.Attached {
		t.Fatalf("sorted view = %+v, want filed under %s at 0.93 and not attached", u, search)
	}
	if got := provisionalIDs(t, s); !slices.Equal(got, []string{"claude-prompt:2"}) {
		t.Fatalf("provisional = %v, want only the unsorted message", got)
	}
}

func TestAnAttachSupersedesASortAndCountsAMissOnlyWhenItDisagrees(t *testing.T) {
	s := newStore(t)
	search := goal(t, s, "search")
	billing := goal(t, s, "billing")
	for i := 1; i <= 3; i++ {
		capture(t, s, project, fmt.Sprintf("claude-prompt:%d", i), fmt.Sprintf("message %d", i))
		sortTo(t, s, fmt.Sprintf("claude-prompt:%d", i), search, 0.95)
	}
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: search, Clauses: []string{"rank exact matches first"}})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsNewAsk, To: billing, Clauses: []string{"send receipts"}})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsNewAsk, To: search, Clauses: []string{"index receipts"}, SessionID: "helper"})
	if u := utteranceView(t, s, "claude-prompt:1"); u.Sorted.Overridden {
		t.Fatalf("an attach to the sorted goal counted as a miss: %+v", u)
	}
	if u := utteranceView(t, s, "claude-prompt:2"); !u.Sorted.Overridden || !u.Attached {
		t.Fatalf("an attach to another goal did not supersede the sort: %+v", u)
	}
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	if got := board.SortStats(); got != (ledger.SortStats{Filed: 3, Overridden: 1}) || got.String() != "auto-filed 3, overridden 1" {
		t.Fatalf("stats = %+v %q", got, got.String())
	}
	if sortTo(t, s, "claude-prompt:1", search, 0.99) {
		t.Fatal("an attached message was sorted")
	}
}

func TestTheSorterSeesTheGoalsAndRequestsAsTheyStoodWhenTheMessageWasRecorded(t *testing.T) {
	s := newStore(t)
	search := goal(t, s, "search")
	capture(t, s, project, "claude-prompt:0", "search work")
	var clauses []string
	for i := 1; i <= 14; i++ {
		clauses = append(clauses, fmt.Sprintf("search request %d", i))
	}
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:0", As: ledger.AsNewAsk, To: search, Clauses: clauses})
	capture(t, s, project, "claude-prompt:1", "make it faster")
	billing := goal(t, s, "billing")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsConversation})
	capture(t, s, project, "claude-prompt:2", "and the invoices")
	input, ok, err := s.SortInputFor(project, "claude-prompt:2")
	if err != nil || !ok {
		t.Fatalf("input for a provisional message: ok=%v err=%v", ok, err)
	}
	if len(input.Goals) != 2 || input.Goals[0].ID != search || input.Goals[1].ID != billing {
		t.Fatalf("goals = %+v, want search then billing", input.Goals)
	}
	if got := input.Goals[0].Requests; len(got) != 12 || got[0] != "search request 3" || got[11] != "search request 14" {
		t.Fatalf("search requests = %v, want the newest 12", got)
	}
	if input.Text != "and the invoices" || input.TargetID != "s1" || input.At.IsZero() {
		t.Fatalf("input = %+v", input)
	}
	if _, ok, err := s.SortInputFor(project, "claude-prompt:1"); ok || err != nil {
		t.Fatalf("an attached message was offered to the sorter: ok=%v err=%v", ok, err)
	}
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsNewAsk, To: billing, Clauses: []string{"itemise invoices"}})
	capture(t, s, project, "claude-prompt:3", "a later thought")
	later, ok, err := s.SortInputFor(project, "claude-prompt:3")
	if err != nil || !ok || !slices.Equal(later.Goals[1].Requests, []string{"itemise invoices"}) {
		t.Fatalf("a later message does not see the requests filed before it: %+v ok=%v err=%v", later, ok, err)
	}
}

func TestNoGoalYetMeansNothingToSort(t *testing.T) {
	s := newStore(t)
	capture(t, s, project, "claude-prompt:1", "start a project")
	goal(t, s, "later")
	if _, ok, err := s.SortInputFor(project, "claude-prompt:1"); ok || err != nil {
		t.Fatalf("a message recorded before any goal was offered: ok=%v err=%v", ok, err)
	}
}

func TestASortReplaysAndAReclassifiedMessageDropsItQuietly(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	search := goal(t, s, "search")
	capture(t, s, project, "claude-prompt:1", "rank exact matches")
	capture(t, s, project, "claude-prompt:2", "scheduled words")
	sortTo(t, s, "claude-prompt:1", search, 0.92)
	sortTo(t, s, "claude-prompt:2", search, 0.92)
	if _, err := s.ReclassifyUtterance(project, "claude-prompt:2", "scheduled", time.Now()); err != nil {
		t.Fatal(err)
	}
	fresh := ledger.NewCachedStore(db, db)
	board, err := fresh.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	if got := board.SortStats(); got.Filed != 1 {
		t.Fatalf("after replay stats = %+v, want the one surviving sort", got)
	}
	if u := utteranceView(t, fresh, "claude-prompt:1"); u.Sorted == nil || u.Sorted.GoalID != search {
		t.Fatalf("the sort did not replay: %+v", u)
	}
	if retractions := board.Retractions(); len(retractions) != 0 {
		t.Fatalf("a sort of reclassified words reads as a retraction: %+v", retractions)
	}
}

func TestOwedSortsStartAtAMessageNotASecondAndGoBackADayAtMost(t *testing.T) {
	s := newStore(t)
	search := goal(t, s, "search")
	now := time.Now()
	var seqs []int64
	for i, at := range []time.Time{now.Add(-24*time.Hour - 500*time.Millisecond), now, now, now, now} {
		u := said(fmt.Sprintf("claude-prompt:%d", i), fmt.Sprintf("message %d", i))
		u.ProjectKey, u.At = project, at
		e, _, err := s.RecordUtterance(u)
		if err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, e.Seq)
	}
	sortTo(t, s, "claude-prompt:3", search, 0.95)
	got, err := s.LocalProvisionalFrom(project, seqs[2], now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []ledger.OwedSort{{Ref: "claude-prompt:2", Seq: seqs[2]}, {Ref: "claude-prompt:4", Seq: seqs[4]}}
	if !slices.Equal(got, want) {
		t.Fatalf("owed = %v, want %v: from the first owed message on, not the one before it in the same second, and not the sorted one", got, want)
	}
	if got, err := s.LocalProvisionalFrom(project, 0, now.Add(-24*time.Hour)); err != nil || len(got) == 0 || got[0].Ref != "claude-prompt:1" {
		t.Fatalf("a message older than a day was owed: %v (%v)", got, err)
	}
}

func TestTheBoardReadCarriesAutoFiledCountsAndTheSorterHealth(t *testing.T) {
	s := newStore(t)
	search := goal(t, s, "search")
	capture(t, s, project, "claude-prompt:1", "rank exact matches")
	plain, err := s.Read(project, ledger.ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain["autoFiled"]; ok {
		t.Fatalf("nothing filed, yet the read has autoFiled: %v", plain["autoFiled"])
	}
	if _, ok := plain["sorter"]; ok {
		t.Fatal("a read without a sorter reports one")
	}
	sortTo(t, s, "claude-prompt:1", search, 0.95)
	since := time.Date(2026, 11, 9, 14, 2, 0, 0, time.UTC)
	read, err := s.Read(project, ledger.ReadRequest{Sorter: &ledger.SorterHealth{Model: "d1-3b", Since: since, Error: "connection refused", Failures: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if got := read["autoFiled"]; got != (ledger.SortStats{Filed: 1}) {
		t.Fatalf("autoFiled = %#v", got)
	}
	health, _ := read["sorter"].(json.RawMessage)
	var got map[string]any
	if err := json.Unmarshal(health, &got); err != nil {
		t.Fatalf("sorter health %q: %v", health, err)
	}
	if got["model"] != "d1-3b" || got["unavailableSince"] != "2026-11-09T14:02:00Z" || got["error"] != "connection refused" || got["failures"] != float64(2) {
		t.Fatalf("sorter health = %v", got)
	}
}
