package registry

import (
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func pendingWithdrawIDs(t *testing.T, r *Registry) []string {
	t.Helper()
	pending, err := r.PendingNotificationWithdraws()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(pending))
	for _, w := range pending {
		ids = append(ids, w.AskID)
	}
	return ids
}

func TestClosingAnAskThePersonMayHaveSeenQueuesItsWithdrawWhicheverWayItCloses(t *testing.T) {
	r := openTestRegistry(t)
	for _, id := range []string{"asker", "lead"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
	}
	raise := func(routedTo string) Ask {
		t.Helper()
		ask, err := r.RaiseAsk(Ask{SessionID: "asker", Origin: AskOriginAgent, Title: "Release?", Questions: []AskQuestion{{Question: "Release?"}}, RoutedTo: routedTo}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return ask
	}

	withdrawn := raise(AskToPerson)
	if _, err := r.WithdrawAsk(withdrawn.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	answered := raise(AskToPerson)
	if claimed, err := r.ClaimAsk(answered.ID, AskAnswer{Answers: map[string]string{"Release?": "Yes"}}, AskToPerson, "dashboard", time.Now()); err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if got := pendingWithdrawIDs(t, r); !slices.Equal(got, []string{withdrawn.ID}) {
		t.Fatalf("pending=%v, want only the withdrawn ask before the answer is applied", got)
	}
	if err := r.RecordAskApplied(answered.ID, "applied", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	escalated := raise("lead")
	if moved, err := r.RouteAsk(escalated.ID, AskToPerson, AskEscalated, time.Now()); err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if moved, err := r.RouteAsk(escalated.ID, "lead", AskEscalated, time.Now()); err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if _, err := r.WithdrawAsk(escalated.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	unseen := raise("lead")
	if _, err := r.WithdrawAsk(unseen.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	deleted := raise(AskToPerson)
	if _, err := r.db.Exec(`DELETE FROM asks WHERE id=?`, deleted.ID); err != nil {
		t.Fatal(err)
	}

	if got, want := pendingWithdrawIDs(t, r), []string{withdrawn.ID, answered.ID, escalated.ID, deleted.ID}; !slices.Equal(got, want) {
		t.Fatalf("pending=%v, want %v and not the ask only its lead saw", got, want)
	}
}

func TestAWithdrawQueuedAgainWhileItIsSentIsStillOwed(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.QueueNotificationWithdraw("mirrored"); err != nil {
		t.Fatal(err)
	}
	read, err := r.PendingNotificationWithdraws()
	if err != nil || len(read) != 1 {
		t.Fatalf("pending=%v err=%v", read, err)
	}
	if err := r.QueueNotificationWithdraw("mirrored"); err != nil {
		t.Fatal(err)
	}
	if cleared, err := r.ClearNotificationWithdraw(read[0]); err != nil || cleared {
		t.Fatalf("cleared=%v err=%v, want the second queue kept", cleared, err)
	}
	again, err := r.PendingNotificationWithdraws()
	if err != nil || len(again) != 1 {
		t.Fatalf("pending=%v err=%v", again, err)
	}
	if cleared, err := r.ClearNotificationWithdraw(again[0]); err != nil || !cleared {
		t.Fatalf("cleared=%v err=%v", cleared, err)
	}
	if got := pendingWithdrawIDs(t, r); len(got) != 0 {
		t.Fatalf("pending=%v", got)
	}
}
