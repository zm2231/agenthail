package ledger_test

import (
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"
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
	if len(export.Cuts) != 0 || export.State == ledger.StateRejected {
		t.Fatalf("a cut raised by the task's words still stands: %+v", export)
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
		var shown bool
		for _, r := range b.Retractions() {
			if r.Kind == ledger.RetractedAsk && r.ID == asks[0].ID {
				shown = r.ReSourcedTo == "claude-prompt:typed" && strings.Contains(r.String(), "re-sourced to claude-prompt:typed")
			}
		}
		if !shown {
			t.Fatalf("%s board: the kept ask does not show it was re-sourced: %+v", name, b.Retractions())
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
			if err := b.Rebuild(events); err != nil {
				t.Fatal(err)
			}
			decisions := b.Decisions()
			if len(decisions) != 1 || decisions[0].Version != tc.wantVersion || decisions[0].Text != tc.wantText || decisions[0].SourceUtterance != "claude-prompt:typed" {
				t.Fatalf("decisions = %+v", decisions)
			}
		})
	}
}

func TestWhatRestsOnRetractedWordsGoesWithThem(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	g := goal(t, s, "Search")
	capture(t, s, project, "claude-prompt:wake", "build search")
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:wake", As: ledger.AsNewAsk, To: g, Clauses: []string{"build search"}})
	q := askQuestion(t, s, ledger.QuestionRequest{To: "widget-A1", Text: "full-text or prefix?", DefaultAnswer: "prefix"})
	items(t, s, "add the index")
	capture(t, s, project, "claude-prompt:typed", "full-text")

	if done, err := s.ReclassifyUtterance(project, "claude-prompt:wake", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	rebuilt, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]*ledger.Board{"cached": board(t, s), "rebuilt": rebuilt} {
		if len(b.Questions()) != 0 || len(b.WorkItems()) != 0 || len(b.AllAsks()) != 0 {
			t.Fatalf("%s board keeps what rested on the retracted ask: questions %+v items %+v", name, b.Questions(), b.WorkItems())
		}
		lines := map[string]ledger.Retraction{}
		for _, r := range b.Retractions() {
			lines[r.Kind+" "+r.ID] = r
		}
		for _, want := range []string{"question " + q.ID, "work-item widget-W1"} {
			r, ok := lines[want]
			if !ok || r.UtteranceID != "claude-prompt:wake" || r.DependsOn == "" || len(r.AttachedBy) != 1 || !strings.Contains(r.String(), "rests on") {
				t.Fatalf("%s board: no notice line for %s: %+v", name, want, b.Retractions())
			}
		}
	}
	var refusal *ledger.Refusal
	if _, err := s.Attach(ledger.AttachRequest{ProjectKey: project, SessionID: "s1", UtteranceID: "claude-prompt:typed", As: ledger.AsAnswers, To: q.ID}); !errors.As(err, &refusal) {
		t.Fatalf("answering the dropped question: want a refusal, got %v", err)
	}
	if _, err := s.Work(ledger.WorkRequest{ProjectKey: project, SessionID: "s1", Action: ledger.WorkClaim, ItemID: "widget-W1"}, time.Now()); !errors.As(err, &refusal) {
		t.Fatalf("claiming the dropped work item: want a refusal, got %v", err)
	}
}

func TestACheckGivenReclassifiedWordsIsVoid(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	g := fuzzySearch(t, s)
	if _, err := s.StartCheck(project, g, "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if done, err := s.ReclassifyUtterance(project, "claude-prompt:2", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	var refusal *ledger.Refusal
	for _, report := range []ledger.CheckReport{
		{ProjectKey: project, Run: "run-1", SessionID: "run-1", Missing: []ledger.Omission{{Breadth: "typo tolerance", From: "claude-prompt:2"}}},
		{ProjectKey: project, Run: "run-1", SessionID: "run-1", None: true},
	} {
		if _, err := s.ReportCheck(report, time.Now()); !errors.As(err, &refusal) {
			t.Fatalf("a report on a check given reclassified words: want a refusal, got %v", err)
		}
	}
	rebuilt, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]*ledger.Board{"cached": board(t, s), "rebuilt": rebuilt} {
		if _, ok := b.CheckRun("run-1"); ok || len(b.OpenChecks()) != 0 || len(b.TargetCuts()) != 0 {
			t.Fatalf("%s board keeps the void check: open %+v cuts %+v", name, b.OpenChecks(), b.TargetCuts())
		}
		var voided bool
		for _, r := range b.Retractions() {
			voided = voided || (r.Kind == ledger.RetractedCheck && r.ID == "run-1" && r.UtteranceID == "claude-prompt:2")
		}
		if !voided {
			t.Fatalf("%s board: no line for the void check: %+v", name, b.Retractions())
		}
	}
	if !reflect.DeepEqual(sortedRetractions(rebuilt.Retractions()), sortedRetractions(board(t, s).Retractions())) {
		t.Fatal("cached and rebuilt retractions differ")
	}
	if _, err := s.StartCheck(project, g, "run-2", time.Now()); err != nil {
		t.Fatalf("the goal cannot be checked again on the person's words: %v", err)
	}
}

func sortedRetractions(rs []ledger.Retraction) []ledger.Retraction {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Kind+rs[i].ID < rs[j].Kind+rs[j].ID })
	return rs
}

func TestALaterApprovalSurvivesRetractingAnEarlierOne(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	searchAsk(t, s)
	items(t, s, "add the index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1"})
	capture(t, s, project, "claude-prompt:wake", "approve it")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:wake"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkReopen, ItemID: "widget-W1", SessionID: "reviewer", Reason: "missed a case"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: "widget-W1"})
	capture(t, s, project, "claude-prompt:typed", "now it is right, approve")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: "widget-W1", UtteranceID: "claude-prompt:typed"})

	if done, err := s.ReclassifyUtterance(project, "claude-prompt:wake", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	rebuilt, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]*ledger.Board{"cached": board(t, s), "rebuilt": rebuilt} {
		for _, item := range b.WorkItems() {
			if item.State != ledger.WorkDone || item.Reviewer != "person" {
				t.Fatalf("%s board: the person's later approval was taken back: %+v", name, item)
			}
		}
	}
}

func TestALaterDelegationToTheSameSessionSurvives(t *testing.T) {
	for _, tc := range []struct {
		name      string
		again     bool
		wantOwner string
	}{
		{"a later delegation to the same session stays", true, "worker"},
		{"a lone delegation gives the clause back", false, "lead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []ledger.Event{
				makeEvent(1, ledger.EventAsk, ledger.AskPayload{AskID: "A1", Type: ledger.AskBuild, Principal: "person"}),
				makeEvent(2, ledger.EventClause, ledger.ClausePayload{ClauseID: "C1", AskID: "A1", Text: "build search", State: ledger.StateOwned, OwnerSessionID: "lead"}),
				makeEvent(3, ledger.EventUtterance, ledger.UtterancePayload{UtteranceID: "claude-prompt:wake", Text: "w", CapturedBy: ledger.CapturedByHook}),
				makeEvent(4, ledger.EventUtterance, ledger.UtterancePayload{UtteranceID: "claude-prompt:typed", Text: "t", CapturedBy: ledger.CapturedByHook}),
				makeEvent(5, ledger.EventDelegation, ledger.DelegationPayload{AskID: "A1", DelegatorSessionID: "lead", DelegateSessionID: "worker", SourceUtteranceID: "claude-prompt:wake"}),
			}
			if tc.again {
				events = append(events, makeEvent(6, ledger.EventDelegation, ledger.DelegationPayload{AskID: "A1", DelegatorSessionID: "lead", DelegateSessionID: "worker", SourceUtteranceID: "claude-prompt:typed"}))
			}
			events = append(events, makeEvent(7, ledger.EventReclassified, ledger.ReclassifiedPayload{UtteranceID: "claude-prompt:wake", Reason: "scheduled"}))
			b := ledger.NewBoard()
			if err := b.Rebuild(events); err != nil {
				t.Fatal(err)
			}
			if owned := b.ClausesOwnedBy(tc.wantOwner); len(owned) != 1 || owned[0].ID != "C1" {
				t.Fatalf("C1 should be owned by %s: %+v", tc.wantOwner, owned)
			}
			asks := b.AllAsks()
			if len(asks) != 1 || slices.Contains(asks[0].Delegates, "worker") != tc.again {
				t.Fatalf("delegates %+v", asks)
			}
		})
	}
}
