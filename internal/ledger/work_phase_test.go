package ledger_test

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const prURL = "https://github.com/acme/widget/pull/12"

func phaseOf(t *testing.T, s *ledger.Store, id string) ledger.Phase {
	t.Helper()
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := board.WorkItem(id)
	if !ok {
		t.Fatalf("%s is not on the board", id)
	}
	return item.Phase
}

func wantPhase(t *testing.T, s *ledger.Store, id string, want ledger.Phase) {
	t.Helper()
	if got := phaseOf(t, s, id); got != want {
		t.Fatalf("%s phase = %q, want %q", id, got, want)
	}
}

func watcherLogs(t *testing.T, s *ledger.Store, id string, mark ledger.PRMark, url string) {
	t.Helper()
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: id, SessionID: holder, Text: ledger.PRLine(mark, url), Source: ledger.WorkSourcePRItem})
}

func verifies(t *testing.T, s *ledger.Store, id string) {
	t.Helper()
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: id, SessionID: holder, LogType: ledger.LogVerify, Text: "running the import against a copy of the real data"})
}

func TestAnItemsPhaseFollowsItsWorkFromScopeToShip(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	capture(t, s, project, "claude-prompt:2", "send it back, the index is missing")
	items(t, s, "add the index")
	id := "widget-W1"
	wantPhase(t, s, id, ledger.PhaseScope)

	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: id, SessionID: holder})
	wantPhase(t, s, id, ledger.PhaseBuild)
	watcherLogs(t, s, id, ledger.PROpened, prURL)
	wantPhase(t, s, id, ledger.PhaseReview)
	verifies(t, s, id)
	wantPhase(t, s, id, ledger.PhaseVerify)

	second := "https://github.com/acme/widget/pull/13"
	watcherLogs(t, s, id, ledger.PROpened, second)
	wantPhase(t, s, id, ledger.PhaseReview)
	verifies(t, s, id)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkBlock, ItemID: id, SessionID: holder, Reason: "waiting on CI"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkUnblock, ItemID: id, SessionID: holder})
	wantPhase(t, s, id, ledger.PhaseVerify)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: id, SessionID: holder})
	wantPhase(t, s, id, ledger.PhaseVerify)

	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: id, SessionID: holder})
	wantPhase(t, s, id, ledger.PhaseCheck)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkReject, ItemID: id, SessionID: "reviewer", Reason: "the index is missing"})
	wantPhase(t, s, id, ledger.PhaseReview)

	watcherLogs(t, s, id, ledger.PRClosed, prURL)
	wantPhase(t, s, id, ledger.PhaseReview)
	watcherLogs(t, s, id, ledger.PRClosed, second)
	wantPhase(t, s, id, ledger.PhaseBuild)

	work(t, s, ledger.WorkRequest{Action: ledger.WorkSubmit, ItemID: id, SessionID: holder})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkApprove, ItemID: id, SessionID: "reviewer"})
	wantPhase(t, s, id, ledger.PhaseShip)
}

func TestOnlyThePRWatchersLinesMoveAPhase(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "add the index")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", SessionID: holder, Text: ledger.PRLine(ledger.PROpened, prURL)})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", SessionID: holder, Text: ledger.PRLine(ledger.PRMerged, prURL)})
	wantPhase(t, s, "widget-W1", ledger.PhaseBuild)
}

func TestAMergedPRShipsAnItemWhoseHolderNeverMarkedItDone(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "add the index")
	watcherLogs(t, s, "widget-W1", ledger.PROpened, prURL)
	watcherLogs(t, s, "widget-W1", ledger.PRMerged, prURL)
	board, err := s.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := board.WorkItem("widget-W1")
	if item.State != ledger.WorkInProgress || item.Phase != ledger.PhaseShip {
		t.Fatalf("state %q phase %q, want in_progress and ship", item.State, item.Phase)
	}
	if merged := lastEntry(item); !item.PhaseSince.Equal(merged.At) {
		t.Fatalf("phaseSince = %v, want the merge line's time %v", item.PhaseSince, merged.At)
	}
}

func TestTakingOverAnItemSetsThePhaseOfWhatItBecomes(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "add the index", "wire the endpoint", "ship the docs")
	verifies(t, s, "widget-W1")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W1", Reason: "archived", Takeover: takeover(ledger.TakeoverRelease, ledger.HolderArchived)})
	wantPhase(t, s, "widget-W1", ledger.PhaseScope)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1", SessionID: holder})
	wantPhase(t, s, "widget-W1", ledger.PhaseBuild)

	work(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W2", Reason: "gone", Takeover: takeover(ledger.TakeoverSubmit, ledger.HolderGone)})
	wantPhase(t, s, "widget-W2", ledger.PhaseCheck)

	watcherLogs(t, s, "widget-W3", ledger.PRMerged, prURL)
	work(t, s, ledger.WorkRequest{Action: ledger.WorkTakeover, ItemID: "widget-W3", Reason: "gone", Takeover: takeover(ledger.TakeoverDone, ledger.HolderGone)})
	wantPhase(t, s, "widget-W3", ledger.PhaseShip)
}

func TestClosedAndDeletedItemsHaveNoPhase(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	items(t, s, "add the index", "wire the endpoint")
	work(t, s, ledger.WorkRequest{Action: ledger.WorkClose, ItemID: "widget-W1", Reason: "duplicate"})
	work(t, s, ledger.WorkRequest{Action: ledger.WorkDelete, ItemID: "widget-W2", Reason: "filed by mistake"})
	wantPhase(t, s, "widget-W1", "")
	wantPhase(t, s, "widget-W2", "")
}

func TestAProjectRenamesPhasesWithoutChangingTheirKeys(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	heldBy(t, s, "add the index")
	set := func(labels map[string]string) (map[ledger.Phase]string, error) {
		return s.SetPhaseLabels(ledger.PhaseLabelsRequest{ProjectKey: project, SessionID: "s1", Labels: labels}, time.Now())
	}
	labels, err := set(map[string]string{"verify": "QA", "check": " Sign-off "})
	if err != nil {
		t.Fatal(err)
	}
	if labels[ledger.PhaseVerify] != "QA" || labels[ledger.PhaseCheck] != "Sign-off" || labels[ledger.PhaseBuild] != "Build" || len(labels) != len(ledger.Phases) {
		t.Fatalf("labels = %v", labels)
	}
	verifies(t, s, "widget-W1")
	wantPhase(t, s, "widget-W1", ledger.PhaseVerify)

	if labels, err = set(map[string]string{"verify": ""}); err != nil || labels[ledger.PhaseVerify] != "Verify" || labels[ledger.PhaseCheck] != "Sign-off" {
		t.Fatalf("labels = %v, %v", labels, err)
	}
	for _, bad := range []map[string]string{{"qa": "QA"}, {"ship": "Released to every customer today"}, {"ship": "two\nlines"}, {}} {
		if _, err := set(bad); err == nil {
			t.Fatalf("labels %v were accepted", bad)
		}
	}
}
