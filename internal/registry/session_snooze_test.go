package registry

import (
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestASessionOrProjectArchiveRefusesASnoozeButNotAWake(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "member"}, `{"id":"member","current":false}`)
	recordEndedRow(t, r, surface.Session{ID: "alone"}, `{"id":"alone","current":false}`)
	if _, err := r.db.Exec(`UPDATE catalog_sessions SET host_project=? WHERE session_id='member'`, []byte(`{"id":"repo:x","localId":"p-x"}`)); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	snooze := Snooze{At: at, Until: at.Add(time.Hour), Wake: SnoozeWakeTime}
	if _, err := r.SnoozeSession("member", snooze); err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveProject(ProjectRecord{ID: "p-x", WorkspaceRoot: "/fixture", Title: "x"}, at); err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveSession("alone", at); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"member", "alone"} {
		if _, err := r.SnoozeSession(id, snooze); !errors.Is(err, ErrSnoozeArchived) {
			t.Fatalf("snoozing archived %s: %v", id, err)
		}
	}
	if previous, err := r.WakeSession("member"); err != nil || previous != snooze {
		t.Fatalf("waking an archived session: %+v %v", previous, err)
	}
}

func TestPinAndSnoozeComeBackFromOneReadAndAWakeReturnsTheSnooze(t *testing.T) {
	r := coordinatorRegistry(t, "pinned", "snoozed", "both", "plain")
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"pinned", "both"} {
		if _, err := r.PinSession(id, at); err != nil {
			t.Fatal(err)
		}
	}
	later := Snooze{At: at, Until: at.Add(time.Hour), Wake: SnoozeWakeTime}
	finished := Snooze{At: at, Wake: SnoozeWakeFinished}
	if previous, err := r.SnoozeSession("snoozed", later); err != nil || previous.Set() {
		t.Fatalf("first snooze: %+v %v", previous, err)
	}
	if _, err := r.SnoozeSession("both", finished); err != nil {
		t.Fatal(err)
	}
	marks, err := r.SessionMarks()
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 3 {
		t.Fatalf("marks %+v; want pinned, snoozed and both", marks)
	}
	if m := marks["snoozed"]; !m.PinnedAt.IsZero() || m.Snooze != later {
		t.Fatalf("snoozed only: %+v", m)
	}
	if m := marks["both"]; !m.PinnedAt.Equal(at) || m.Snooze != finished {
		t.Fatalf("pinned and snoozed: %+v", m)
	}
	if m := marks["pinned"]; !m.PinnedAt.Equal(at) || m.Snooze.Set() {
		t.Fatalf("pinned only: %+v", m)
	}
	previous, err := r.WakeSession("snoozed")
	if err != nil || previous != later {
		t.Fatalf("wake handed back %+v %v", previous, err)
	}
	if marks, _ := r.SessionMarks(); marks["snoozed"].Snooze.Set() {
		t.Fatal("a woken session stayed snoozed")
	}
	if _, err := r.WakeSession("missing"); err == nil {
		t.Fatal("waking an unknown session succeeded")
	}
}
