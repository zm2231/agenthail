package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestALimitStopHoldsTheQueueUntilItIsReleased(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithKey(session.ID, "next step", ""); err != nil {
		t.Fatal(err)
	}
	resets := time.Date(2026, 10, 23, 3, 20, 0, 0, time.UTC)
	stop := surface.LimitStop{TurnID: "u1", RefusedAt: resets.Add(-time.Hour), ResetsAt: resets}
	if err := r.RecordLimitStop(session, "/roots/work", stop); err != nil {
		t.Fatal(err)
	}
	now := resets.Add(-time.Minute)
	if claimed, err := r.ClaimNextMessage(session.ID, now); err != nil || claimed != nil {
		t.Fatalf("a limited session's queue was claimed: %+v err=%v", claimed, err)
	}
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 || rows[0].HeldBy != QueueHeldLimited {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	resumes, err := r.LimitResumes()
	if err != nil || len(resumes) != 1 || resumes[0].AccountKey != "/roots/work" || resumes[0].State != LimitResumeWaiting || !resumes[0].ResetsAt.Equal(resets) || resumes[0].TurnID != "u1" {
		t.Fatalf("resumes=%+v err=%v", resumes, err)
	}

	if err := r.RecordLimitStop(session, "/roots/work", stop); err != nil {
		t.Fatal(err)
	}
	if err := r.SetLimitResumeState(session.ID, "u1", LimitResumeReleased, now); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordLimitStop(session, "/roots/work", stop); err != nil {
		t.Fatal(err)
	}
	if resumes, _ := r.LimitResumes(); resumes[0].State != LimitResumeReleased {
		t.Fatalf("observing the same stop again reset its state: %+v", resumes)
	}
	if claimed, err := r.ClaimNextMessage(session.ID, now); err != nil || claimed == nil || claimed.Message != "next step" {
		t.Fatalf("a released session's queue was not claimed: %+v err=%v", claimed, err)
	}
}

func TestANewRefusalStartsANewStopAndAClearedStopIsGone(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	first := surface.LimitStop{TurnID: "u1", RefusedAt: time.Unix(1000, 0)}
	if err := r.RecordLimitStop(session, "", first); err != nil {
		t.Fatal(err)
	}
	if err := r.SetLimitResumeState(session.ID, "u1", LimitResumeReleased, time.Unix(2000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := r.SetLimitResumeState(session.ID, "other", LimitResumeCanceled, time.Unix(2000, 0)); err != nil {
		t.Fatal(err)
	}
	if resumes, _ := r.LimitResumes(); resumes[0].State != LimitResumeReleased {
		t.Fatalf("a state change for another turn applied: %+v", resumes)
	}
	second := surface.LimitStop{TurnID: "u2", RefusedAt: time.Unix(3000, 0), ResetsAt: time.Unix(9000, 0)}
	if err := r.RecordLimitStop(session, "", second); err != nil {
		t.Fatal(err)
	}
	resumes, err := r.LimitResumes()
	if err != nil || len(resumes) != 1 || resumes[0].TurnID != "u2" || resumes[0].State != LimitResumeWaiting || resumes[0].Attempts != 1 {
		t.Fatalf("resumes=%+v err=%v", resumes, err)
	}
	if err := r.ClearLimitStop(session.ID, "u1"); err != nil {
		t.Fatal(err)
	}
	if resumes, _ := r.LimitResumes(); len(resumes) != 1 {
		t.Fatalf("clearing an older turn removed the current stop: %+v", resumes)
	}
	if err := r.ClearLimitStop(session.ID, "u2"); err != nil {
		t.Fatal(err)
	}
	if resumes, _ := r.LimitResumes(); len(resumes) != 0 {
		t.Fatalf("resumes=%+v", resumes)
	}
}

func TestACanceledResumeIsKeptForItsTurn(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	stop := surface.LimitStop{TurnID: "u1", RefusedAt: time.Unix(1000, 0)}
	if err := r.RecordLimitStop(session, "/roots/work", stop); err != nil {
		t.Fatal(err)
	}
	if found, err := r.CancelLimitResume(session.ID); err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if err := r.RecordLimitStop(session, "/roots/work", stop); err != nil {
		t.Fatal(err)
	}
	if resumes, _ := r.LimitResumes(); resumes[0].State != LimitResumeCanceled {
		t.Fatalf("a canceled resume came back: %+v", resumes)
	}
	if found, err := r.CancelLimitResume("absent"); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestALaterResetOnAnAccountMovesEveryWaitingSessionOnIt(t *testing.T) {
	r := openTestRegistry(t)
	for _, id := range []string{"first", "second", "elsewhere"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
			t.Fatal(err)
		}
	}
	early, late := time.Unix(5000, 0), time.Unix(9000, 0)
	for _, row := range []struct{ id, account string }{{"first", "work"}, {"second", "work"}, {"elsewhere", "home"}} {
		if err := r.RecordLimitStop(surface.Session{ID: row.id}, row.account, surface.LimitStop{TurnID: "t", RefusedAt: time.Unix(1000, 0), ResetsAt: early}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RecordLimitStop(surface.Session{ID: "first"}, "work", surface.LimitStop{TurnID: "t2", RefusedAt: time.Unix(6000, 0), ResetsAt: late}); err != nil {
		t.Fatal(err)
	}
	resumes, err := r.LimitResumes()
	if err != nil {
		t.Fatal(err)
	}
	for _, resume := range resumes {
		want := late
		if resume.SessionID == "elsewhere" {
			want = early
		}
		if !resume.ResetsAt.Equal(want) {
			t.Fatalf("%s resets at %s, want %s", resume.SessionID, resume.ResetsAt, want)
		}
	}
}
