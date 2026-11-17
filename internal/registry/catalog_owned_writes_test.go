package registry

import (
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func recordOwnedCatalogWrite(r *Registry, session surface.Session, update SessionUpdate) error {
	projection := `{"id":"` + session.ID + `","status":"` + string(session.Status) + `"}`
	_, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: projection, Update: &update}, CatalogEvent{DedupeKey: "owned:" + session.ID + ":" + projection, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)})
	return err
}

func TestAStatusWriteFromAnOlderReadKeepsARegistrationThatLandedAfterIt(t *testing.T) {
	r := openTestRegistry(t)
	read := surface.Session{ID: "owned-status", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: 4242, Status: surface.StatusIdle}
	if err := r.RecordObservedSession(read); err != nil {
		t.Fatal(err)
	}
	hosted := read
	hosted.RuntimeKind = surface.RuntimeClaudeHost
	if err := r.RecordObservedSession(hosted); err != nil {
		t.Fatal(err)
	}
	busy := read
	busy.Status = surface.StatusBusy
	if err := recordOwnedCatalogWrite(r, busy, OwnedSessionUpdate(read, SessionActivityFields)); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("err=%v, want the write refused because the row changed after it was read", err)
	}
	stored, err := r.Session(read.ID)
	if err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost || stored.Status != surface.StatusIdle {
		t.Fatalf("stored=%+v err=%v, want the hosted registration kept as it was", stored, err)
	}
	hostedBusy := hosted
	hostedBusy.Status = surface.StatusBusy
	if err := recordOwnedCatalogWrite(r, hostedBusy, OwnedSessionUpdate(hosted, SessionActivityFields)); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.Session(read.ID); err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost || stored.Status != surface.StatusBusy {
		t.Fatalf("stored=%+v err=%v, want the status a write from the current row reads written", stored, err)
	}
}

func TestARestoreKeepsARegistrationThatBroughtTheProcessBack(t *testing.T) {
	r := openTestRegistry(t)
	read := surface.Session{ID: "owned-restore", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusUnknown}
	if err := r.RecordObservedSession(read); err != nil {
		t.Fatal(err)
	}
	live := read
	live.PID, live.Status = 4343, surface.StatusBusy
	if err := r.RecordObservedSession(live); err != nil {
		t.Fatal(err)
	}
	ended := read
	ended.Status, ended.PID, ended.Bridge = surface.StatusOffline, 0, ""
	if err := recordOwnedCatalogWrite(r, ended, SessionUpdate{From: read, Owns: SessionActivityFields | SessionProcessFields, Guard: AllSessionFields}); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("err=%v, want the restore refused because the process came back after it was read", err)
	}
	if stored, err := r.Session(read.ID); err != nil || stored.PID != 4343 || stored.Status != surface.StatusBusy {
		t.Fatalf("stored=%+v err=%v, want the live registration kept", stored, err)
	}
}

func TestAWriterRecordsOnlyWhatItChangedAndKeepsARegistrationThatLandedAfterItsRead(t *testing.T) {
	r := openTestRegistry(t)
	read := surface.Session{ID: "changed-model", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: 4444, Status: surface.StatusIdle}
	if err := r.RecordObservedSession(read); err != nil {
		t.Fatal(err)
	}
	hosted := read
	hosted.RuntimeKind, hosted.PID = surface.RuntimeClaudeHost, 4545
	if err := r.RecordObservedSession(hosted); err != nil {
		t.Fatal(err)
	}
	switched := read
	switched.ConfiguredModel = "opus"
	if err := r.UpdateSession(switched, EditedSessionUpdate(read, switched)); err != nil {
		t.Fatal(err)
	}
	stored, err := r.Session(read.ID)
	if err != nil || stored.ConfiguredModel != "opus" || stored.RuntimeKind != surface.RuntimeClaudeHost || stored.PID != 4545 {
		t.Fatalf("stored=%+v err=%v, want the model written and the hosted registration kept", stored, err)
	}
	busy := read
	busy.Status = surface.StatusBusy
	if err := r.UpdateSession(busy, EditedSessionUpdate(read, busy)); err != nil {
		t.Fatal(err)
	}
	offline := read
	offline.Status = surface.StatusOffline
	if err := r.UpdateSession(offline, EditedSessionUpdate(read, offline)); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("err=%v, want a status change read before another writer's refused", err)
	}
	if stored, err := r.Session(read.ID); err != nil || stored.Status != surface.StatusBusy {
		t.Fatalf("stored=%+v err=%v, want the other writer's status kept", stored, err)
	}
}
