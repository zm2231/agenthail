package ledger_test

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestParkTakesEachKindWithItsWhy(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	add := func(text string) string {
		return work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: text}).Item.ID
	}
	usage, idea, later, person := add("experiment"), add("idea"), add("later"), add("decide")
	revisit := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: usage}, "park needs a kind")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: usage, Park: &ledger.Park{Kind: "soon"}}, "is not one of")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: usage, Park: &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "gemini"}}}, "claude or codex")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: idea, Park: &ledger.Park{Kind: ledger.ParkIdea, Idea: &ledger.ParkIdeaShape{}}}, "keeps the person's words")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: idea, Park: &ledger.Park{Kind: ledger.ParkIdea, Idea: &ledger.ParkIdeaShape{UtteranceID: "claude-prompt:404"}}}, "not a captured utterance")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: later, Park: &ledger.Park{Kind: ledger.ParkLater}}, "needs why")
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: person, Park: &ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: " "}}}, "the question the person has to decide")

	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: usage, Park: &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "claude", Root: "bb"}}})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: idea, Park: &ledger.Park{Kind: ledger.ParkIdea, Idea: &ledger.ParkIdeaShape{UtteranceID: "claude-prompt:1", Goal: "search everywhere", FirstTest: "one query hits two projects"}}})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: later, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "after the release", Revisit: revisit}})
	result := work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: person, Park: &ledger.Park{Kind: ledger.ParkPerson, Person: &ledger.ParkQuestion{Question: "titles only, or bodies too?"}}})
	if result.Readback != "intent ledger: "+person+" parked (person: waits on the person: titles only, or bodies too?) (widget-C1)" {
		t.Fatalf("readback = %q", result.Readback)
	}

	board := rebuild(t, s)
	for id, kind := range map[string]ledger.ParkKind{usage: ledger.ParkUsage, idea: ledger.ParkIdea, later: ledger.ParkLater, person: ledger.ParkPerson} {
		item, ok := board.WorkItem(id)
		if !ok || item.State != ledger.WorkParked || item.Park == nil || item.Park.Kind != kind || item.Park.Session != "s1" {
			t.Fatalf("%s after a rebuild = %+v", id, item)
		}
	}
	if item, _ := board.WorkItem(later); !item.Park.Revisit.Equal(revisit) || item.Park.Why != "after the release" {
		t.Fatalf("later = %+v", item.Park)
	}
	if item, _ := board.WorkItem(idea); item.Park.Idea.UtteranceID != "claude-prompt:1" || item.Park.Idea.Goal != "search everywhere" {
		t.Fatalf("idea = %+v", item.Park.Idea)
	}
	for _, item := range board.Ready() {
		t.Fatalf("parked work is offered as ready: %+v", item)
	}
}

func TestParkingClaimedWorkKeepsAHandoffAndOnlyTheAssigneeOrItsStandInParks(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	id := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "long run"}).Item.ID
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: id})
	usage := &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "codex"}}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: id, SessionID: "lead", Park: usage}, "only its assignee parks it")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: id, SessionID: "lead", ForSession: "s1", Park: usage,
		Handoff: ledger.Handoff{Done: []string{"half the sweep"}, Remaining: []string{"the other half"}}})

	item, _ := rebuild(t, s).WorkItem(id)
	if item.State != ledger.WorkParked || item.Assignee != "" || item.Park.Session != "s1" || item.Handoff == nil || item.Handoff.By != "s1" || item.Handoff.Remaining[0] != "the other half" {
		t.Fatalf("parked = %+v handoff=%+v", item, item.Handoff)
	}
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: id}, "claim needs it open")
	result := work(t, s, ledger.WorkRequest{Action: ledger.WorkUnpark, ItemID: id, SessionID: "lead"})
	if result.Item.State != ledger.WorkOpen || result.Item.Park != nil || result.Item.Handoff == nil {
		t.Fatalf("unparked = %+v", result.Item)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: id, SessionID: "s2"})
}

func TestParkedWorkHoldsNoTurnAndItsDependentsKeepWaiting(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	id := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "first"}).Item.ID
	next := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "then"}).Item.ID
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDep, ItemID: next, DependsOn: id})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: next, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "after first"}})
	gate(t, s, "t0", quiet)
	if r := gate(t, s, "t1", quiet.Add(time.Minute)); r.Result != ledger.GateBlocked {
		t.Fatalf("a clause with open work still holds the turn: %+v", r)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkPark, ItemID: id, Park: &ledger.Park{Kind: ledger.ParkLater, Why: "not now"}})
	gate(t, s, "t2", quiet.Add(2*time.Minute))
	if r := gate(t, s, "t3", quiet.Add(3*time.Minute)); r.Result == ledger.GateBlocked {
		t.Fatalf("a clause whose work is all parked holds the turn: %+v", r)
	}
	work(t, s, ledger.WorkRequest{Action: ledger.WorkUnpark, ItemID: next})
	workRefused(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: next}, "waits on "+id)
}

func rebuild(t *testing.T, s *ledger.Store) *ledger.Board {
	t.Helper()
	events, err := s.Events(project, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	board := ledger.NewBoard()
	if err := board.Rebuild(events); err != nil {
		t.Fatal(err)
	}
	return board
}
