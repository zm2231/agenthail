package registry

import (
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func currentHeadline(t *testing.T, r *Registry, id string) string {
	t.Helper()
	ends, err := r.CurrentTurnEnds()
	if err != nil {
		t.Fatal(err)
	}
	return ends[id].Headline
}

func TestATurnEndsOnceAndStandsUntilTheNextTurn(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	now := time.Now()
	if err := r.OpenTurn("worker", now); err != nil {
		t.Fatal(err)
	}
	if err := r.OpenTurn("worker", now); err != nil {
		t.Fatal("a second copy of the start hook failed:", err)
	}
	end := TurnEnd{SessionID: "worker", Headline: "Deploy?", Source: "tool"}
	if _, err := r.EndTurn(end, "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EndTurn(end, "", now.Add(2*time.Second)); !errors.Is(err, ErrTurnAlreadyEnded) {
		t.Fatalf("a second end in one turn: %v", err)
	}
	if got := currentHeadline(t, r, "worker"); got != "Deploy?" {
		t.Fatalf("current end %q", got)
	}
	if err := r.BindTurnCompletion("worker", "reply-1"); err != nil {
		t.Fatal(err)
	}
	bound, found, err := r.CompletedTurnEnd("worker", "reply-1")
	if err != nil || !found || bound.Headline != "Deploy?" {
		t.Fatalf("completion bound %+v found=%v err=%v", bound, found, err)
	}
	if err := r.SaveRuntimeState("worker", surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "reply-1"}); err != nil {
		t.Fatal(err)
	}
	if got := currentHeadline(t, r, "worker"); got != "Deploy?" {
		t.Fatalf("the end did not stand after its turn completed: %q", got)
	}

	if err := r.OpenTurn("worker", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := currentHeadline(t, r, "worker"); got != "" {
		t.Fatalf("the next turn started and the old end still stands: %q", got)
	}
	if _, err := r.EndTurn(TurnEnd{SessionID: "worker", Headline: "Deployed", Source: "tool"}, "", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := currentHeadline(t, r, "worker"); got != "Deployed" {
		t.Fatalf("current end %q", got)
	}
}

func TestWithoutAStartHookANewTurnIsToldByItsCompletionOrItsRuntimeTurn(t *testing.T) {
	r := coordinatorRegistry(t, "thread")
	now := time.Now()
	end := TurnEnd{SessionID: "thread", Headline: "one", Source: "tool"}
	if _, err := r.EndTurn(end, "turn-1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EndTurn(end, "turn-1", now.Add(time.Second)); !errors.Is(err, ErrTurnAlreadyEnded) {
		t.Fatalf("a second end of turn-1: %v", err)
	}
	end.Headline = "two"
	if _, err := r.EndTurn(end, "turn-2", now.Add(2*time.Second)); err != nil {
		t.Fatal("a different runtime turn was refused:", err)
	}
	if err := r.BindTurnCompletion("thread", "turn-2"); err != nil {
		t.Fatal(err)
	}
	end.Headline = "three"
	if _, err := r.EndTurn(end, "", now.Add(3*time.Second)); err != nil {
		t.Fatal("a turn after a completion was refused:", err)
	}
	if err := r.SaveRuntimeState("thread", surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-9"}); err != nil {
		t.Fatal(err)
	}
	if err := r.BindTurnCompletion("thread", "turn-8"); err != nil {
		t.Fatal(err)
	}
	if got := currentHeadline(t, r, "thread"); got != "" {
		t.Fatalf("a later completion left %q standing", got)
	}
}

func TestAnEndGivenAfterTheNudgeIsRecordedAsNudged(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	now := time.Now()
	for _, opened := range []bool{true, false} {
		if opened {
			if err := r.OpenTurn("worker", now); err != nil {
				t.Fatal(err)
			}
		}
		if ended, err := r.TurnEnded("worker", ""); err != nil || ended {
			t.Fatalf("opened=%v: a turn without an end reads ended=%v err=%v", opened, ended, err)
		}
		if err := r.MarkTurnNudged("worker", now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		end, err := r.EndTurn(TurnEnd{SessionID: "worker", Headline: "Done", Source: TurnSourceTool}, "turn-1", now.Add(2*time.Second))
		if err != nil || end.Source != TurnSourceNudged {
			t.Fatalf("opened=%v: end %+v err=%v", opened, end, err)
		}
		if ended, err := r.TurnEnded("worker", "turn-1"); err != nil || !ended {
			t.Fatalf("opened=%v: the ended turn reads ended=%v err=%v", opened, ended, err)
		}
		if ended, _ := r.TurnEnded("worker", "turn-2"); ended {
			t.Fatalf("opened=%v: another turn reads as ended", opened)
		}
		if err := r.BindTurnCompletion("worker", "reply-1"); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
}
