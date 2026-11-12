package ledger_test

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAUsageParkedItemReadsReadyOnceItsProviderResets(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	id := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "long sweep"}).Item.ID
	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: id, Park: &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "claude", Root: "bb"}}})
	reset := time.Now().Add(3 * time.Hour)
	var asked []ledger.ParkUsageTarget
	reading := func(now time.Time) ledger.UsageReader {
		return func(target ledger.ParkUsageTarget) ledger.Headroom {
			asked = append(asked, target)
			if now.Before(reset) {
				return ledger.Headroom{Known: true, Frees: reset}
			}
			return ledger.Headroom{Known: true, Room: true}
		}
	}
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(time.Hour)
	entries := board.Backlog(ledger.BacklogQuery{}, before, ledger.Readings{Usage: reading(before)})
	if len(entries) != 1 || entries[0].Ready || !entries[0].ReadyAt.Equal(reset) || entries[0].Why != "claude:bb is at its limit until "+reset.UTC().Format(time.RFC3339) {
		t.Fatalf("before the reset = %+v", entries)
	}
	if len(asked) != 1 || asked[0].Provider != "claude" || asked[0].Root != "bb" {
		t.Fatalf("asked for %+v", asked)
	}
	after := reset.Add(time.Minute)
	entries = board.Backlog(ledger.BacklogQuery{Ready: true}, after, ledger.Readings{Usage: reading(after)})
	if len(entries) != 1 || entries[0].Item.ID != id || !entries[0].Ready || entries[0].Why != "claude:bb has headroom" {
		t.Fatalf("after the reset = %+v", entries)
	}
	if entries := board.Backlog(ledger.BacklogQuery{}, after, ledger.Readings{}); entries[0].Ready || entries[0].Why != "no usage reading for claude:bb" {
		t.Fatalf("with no reading = %+v", entries)
	}
}

func TestTheBacklogSaysWhenEachKindCanComeBack(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	add := func(text string) string {
		return work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: text}).Item.ID
	}
	dated, undated, idea, person, blocked, first, stamped := add("dated"), add("undated"), add("idea"), add("person"), add("after first"), add("first"), add("stamped")
	revisit := time.Now().Add(48 * time.Hour)
	park := func(id string, p ledger.Park) {
		work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: id, Park: &p})
	}
	park(dated, ledger.Park{Kind: ledger.ParkLater, Why: "after the release", Revisit: revisit})
	park(undated, ledger.Park{Kind: ledger.ParkLater, Why: "some day"})
	park(idea, ledger.Park{Kind: ledger.ParkIdea, Idea: &ledger.ParkIdeaShape{UtteranceID: "claude-prompt:1"}})
	park(person, ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only?"}})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: blocked, DependsOn: first})
	park(blocked, ledger.Park{Kind: ledger.ParkLater, Why: "after first", Revisit: time.Now().Add(-time.Hour)})
	park(stamped, ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "codex", ResetsAt: revisit}})

	read := func(now time.Time, q ledger.BacklogQuery) map[string]ledger.BacklogEntry {
		board, err := s.Board(project)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]ledger.BacklogEntry{}
		for _, entry := range board.Backlog(q, now, ledger.Readings{}) {
			out[entry.Item.ID] = entry
		}
		return out
	}
	now := read(time.Now(), ledger.BacklogQuery{})
	if len(now) != 6 {
		t.Fatalf("backlog = %+v", now)
	}
	for _, id := range []string{dated, undated, idea, person, stamped} {
		if now[id].Ready {
			t.Fatalf("%s is ready now: %+v", id, now[id])
		}
	}
	if now[blocked].Ready || now[blocked].Why != "waits on "+first {
		t.Fatalf("parked work with an open dependency = %+v", now[blocked])
	}
	if !now[dated].ReadyAt.Equal(revisit) || !now[stamped].ReadyAt.Equal(revisit) {
		t.Fatalf("dated = %+v stamped = %+v", now[dated], now[stamped])
	}
	if at := read(revisit, ledger.BacklogQuery{Ready: true}); len(at) != 2 {
		t.Fatalf("ready at the exact revisit and reset = %+v", at)
	}
	if before := read(revisit.Add(-time.Nanosecond), ledger.BacklogQuery{Ready: true}); len(before) != 0 {
		t.Fatalf("ready just before the revisit = %+v", before)
	}
	later := read(revisit.Add(time.Minute), ledger.BacklogQuery{Ready: true})
	if len(later) != 2 || !later[dated].Ready || !later[stamped].Ready {
		t.Fatalf("ready once the dates pass = %+v", later)
	}
	if kinds := read(time.Now(), ledger.BacklogQuery{Kinds: []ledger.ParkKind{ledger.ParkIdea, ledger.ParkPerson}}); len(kinds) != 2 || kinds[idea].Item.ID == "" || kinds[person].Item.ID == "" {
		t.Fatalf("--kind idea,person = %+v", kinds)
	}

	result, err := s.Read(project, ledger.ReadRequest{View: "backlog", Now: revisit.Add(time.Minute), Backlog: ledger.BacklogQuery{Ready: true}})
	if err != nil {
		t.Fatal(err)
	}
	rows := result["backlog"].([]map[string]any)
	if result["total"] != 2 || len(rows) != 2 || rows[0]["ready"] != true || rows[0]["kind"] != ledger.ParkLater {
		t.Fatalf("backlog view = %+v", result)
	}
	if _, err := s.Read(project, ledger.ReadRequest{View: "backlog", Backlog: ledger.BacklogQuery{Kinds: []ledger.ParkKind{"soon"}}}); err == nil {
		t.Fatal("an unknown kind was accepted")
	}
}

func TestTriageListsUnownedWorkThatHasSatIdle(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	add := func(text string) string {
		return work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: text}).Item.ID
	}
	idle, held, blocked, parked := add("idle"), add("held"), add("blocked"), add("parked")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: held})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkBlock, ItemID: blocked, Reason: "waits on a vendor"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: parked, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "not now"}})

	result, err := s.Read(project, ledger.ReadRequest{View: "triage", Now: time.Now().Add(15 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	rows := result["triage"].([]map[string]any)
	if len(rows) != 2 || rows[0]["id"] != idle || rows[1]["id"] != blocked || result["idleDays"] != 14 {
		t.Fatalf("triage = %+v", result)
	}
	result, err = s.Read(project, ledger.ReadRequest{View: "triage", Now: time.Now().Add(15 * 24 * time.Hour), Idle: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if rows := result["triage"].([]map[string]any); len(rows) != 0 || result["idleDays"] != 30 {
		t.Fatalf("triage --days 30 = %+v", result)
	}
}
