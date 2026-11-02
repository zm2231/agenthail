package ledger_test

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestReclassifiedWordsTakeBackWhatWasBuiltFromThem(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	g := searchAsk(t, s)
	q := askQuestion(t, s, ledger.QuestionRequest{To: ledger.ProjectTarget, Text: "publish the search beta?", Kind: ledger.QuestionOutward})
	capture(t, s, project, "claude-prompt:wake", "yes, and add filters")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:wake", As: ledger.AsAnswers, To: q.ID})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:wake", As: ledger.AsNewAsk, To: g, Clauses: []string{"add filters"}})
	if len(board(t, s).AllAsks()) != 2 || question(t, s, q.ID).State != ledger.QuestionAnswered {
		t.Fatal("setup: the words should have answered the question and added an ask")
	}

	if done, err := s.ReclassifyUtterance(project, "claude-prompt:wake", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}

	cached := board(t, s)
	asks := cached.AllAsks()
	if len(asks) != 1 || asks[0].SourceUtterance != "claude-prompt:1" {
		t.Fatalf("an ask built from the wakeup survived: %+v", asks)
	}
	if reopened := question(t, s, q.ID); reopened.State != ledger.QuestionOpen || reopened.AnsweredBy != "" {
		t.Fatalf("the question the wakeup answered is still answered: %+v", reopened)
	}
	kinds := map[string]ledger.Retraction{}
	for _, r := range cached.Retractions() {
		if r.UtteranceID != "claude-prompt:wake" || r.Reason != "scheduled" || len(r.AttachedBy) != 1 || r.AttachedBy[0] != "s1" {
			t.Fatalf("retraction does not say what and who: %+v", r)
		}
		kinds[r.Kind] = r
	}
	for _, kind := range []string{ledger.RetractedAnswer, ledger.RetractedAsk, ledger.RetractedClause} {
		if _, ok := kinds[kind]; !ok {
			t.Fatalf("no %s retraction in %+v", kind, cached.Retractions())
		}
	}
	if !kinds[ledger.RetractedAnswer].Outward {
		t.Fatal("an outward answer was not flagged as having authorized an act")
	}
	refused(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:wake", As: ledger.AsConversation}, "not a captured person utterance")

	rebuilt, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedRetractions(rebuilt.Retractions()), sortedRetractions(cached.Retractions())) || len(rebuilt.AllAsks()) != len(asks) {
		t.Fatalf("rebuilt board differs:\n cached %+v\nrebuilt %+v", cached.Retractions(), rebuilt.Retractions())
	}
}

func TestReclassifiedWordsUndoAnApprovalAnAckAndARejection(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"build export"}})
	attach(t, s, ledger.AttachRequest{As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"cache results"}, Source: ledger.SourceProposed})
	items(t, s, "add the index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1"})
	capture(t, s, project, "claude-prompt:task", "approve it, drop export, ok and drop caching")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:task"})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:task", As: ledger.AsReacts, To: "widget-C2", Reject: true})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:task", As: ledger.AsReacts, To: "widget-X1", Ack: true})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:task", As: ledger.AsReacts, To: "widget-C3", Reject: true})

	if done, err := s.ReclassifyUtterance(project, "claude-prompt:task", "task_notification", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	b := board(t, s)
	export, caching := clause(t, b, "widget-C2"), clause(t, b, "widget-C3")
	if len(export.Cuts) != 1 || export.Cuts[0].Acked || !export.Cuts[0].Withdrawn {
		t.Fatalf("a cut raised and acked by the task's words still stands: %+v", export.Cuts)
	}
	if caching.State == ledger.StateRejected {
		t.Fatalf("the task's words still reject a proposed clause: %+v", caching)
	}
	var reviewed bool
	for _, r := range b.Retractions() {
		if r.Kind == ledger.RetractedReview && r.ID == "widget-W1" && r.Outward {
			reviewed = true
		}
	}
	if !reviewed {
		t.Fatalf("the approval was not taken back and flagged: %+v", b.Retractions())
	}
	if item := work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", SessionID: "reviewer"}); item.Item.State != ledger.WorkDone {
		t.Fatalf("the item should be back in review for a real reviewer: %+v", item.Item)
	}
}

func TestAnAskKeepsWhatThePersonLaterAddedToIt(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	g := goal(t, s, "Search")
	capture(t, s, project, "claude-prompt:wake", "build search")
	capture(t, s, project, "claude-prompt:typed", "and add filters")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:wake", As: ledger.AsNewAsk, To: g, Clauses: []string{"build search"}})
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:typed", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"add filters"}})

	if done, err := s.ReclassifyUtterance(project, "claude-prompt:wake", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	rebuilt, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]*ledger.Board{"cached": board(t, s), "rebuilt": rebuilt} {
		asks := b.AllAsks()
		if len(asks) != 1 || asks[0].SourceUtterance != "claude-prompt:typed" || len(asks[0].Clauses) != 1 || asks[0].Clauses[0].Text != "add filters" {
			t.Fatalf("%s board: the person's later clause or its ask was lost: %+v", name, asks)
		}
		if got := asks[0].Clauses[0].Sources; len(got) != 1 || got[0] != "claude-prompt:typed" {
			t.Fatalf("%s board: sources %v", name, got)
		}
	}
}

func TestADecisionKeepsTheVersionThePersonGave(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, wantText string
		wantVersion                   int
	}{
		{"retracted first version", "claude-prompt:wake", "claude-prompt:typed", "use postgres 16", 2},
		{"retracted later version", "claude-prompt:typed", "claude-prompt:wake", "use postgres", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := ledger.NewBoard()
			events := []ledger.Event{
				makeEvent(1, ledger.EventUtterance, ledger.UtterancePayload{UtteranceID: "claude-prompt:wake", Text: "w", CapturedBy: ledger.CapturedByHook}),
				makeEvent(2, ledger.EventUtterance, ledger.UtterancePayload{UtteranceID: "claude-prompt:typed", Text: "t", CapturedBy: ledger.CapturedByHook}),
				makeEvent(3, ledger.EventDecision, ledger.DecisionPayload{DecisionID: "D1", Version: 1, Text: "use postgres", Principal: "person", SourceUtterance: tc.first}),
				makeEvent(4, ledger.EventDecisionVer, ledger.DecisionPayload{DecisionID: "D1", Version: 2, Text: "use postgres 16", Principal: "person", SourceUtterance: tc.second}),
				makeEvent(5, ledger.EventReclassified, ledger.ReclassifiedPayload{UtteranceID: "claude-prompt:wake", Reason: "scheduled"}),
			}
			for _, e := range events {
				if err := b.Apply(e); err != nil {
					t.Fatal(err)
				}
			}
			decisions := b.Decisions()
			if len(decisions) != 1 || decisions[0].Version != tc.wantVersion || decisions[0].Text != tc.wantText || decisions[0].SourceUtterance != "claude-prompt:typed" {
				t.Fatalf("decisions = %+v", decisions)
			}
		})
	}
}

func sortedRetractions(rs []ledger.Retraction) []ledger.Retraction {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Kind+rs[i].ID < rs[j].Kind+rs[j].ID })
	return rs
}
