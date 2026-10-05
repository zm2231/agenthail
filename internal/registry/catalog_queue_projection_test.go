package registry

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func catalogQueueTestState(id, name string, queueCount int) (CatalogSessionState, CatalogEvent) {
	fingerprint := map[string]any{
		"id": id, "surface": "claude", "name": name, "status": "idle",
		"queueCount": queueCount, "open": false, "current": queueCount > 0,
	}
	encoded, _ := json.Marshal(fingerprint)
	return CatalogSessionState{
			Session:     surface.Session{ID: id, Surface: surface.KindClaude, Name: name, Status: surface.StatusIdle},
			HostProject: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now().UTC(),
			ProjectionFingerprint: string(encoded),
		}, CatalogEvent{
			DedupeKey: "session.upserted:" + id, Type: "session.upserted", EntityID: id,
			Payload: []byte(`{"session":` + string(encoded) + `}`),
		}
}

func TestUpdateCatalogSessionProjectionRejectsQueueCountCapturedBeforeDrain(t *testing.T) {
	r := openTestRegistry(t)
	state, event := catalogQueueTestState("queue-update-race", "Queue update", 0)
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		t.Fatal(err)
	}
	queueID, err := r.QueueMessageWithKey(state.Session.ID, "queued", "queue-update-race:message")
	if err != nil {
		t.Fatal(err)
	}
	captured, err := r.QueueCounts()
	if err != nil || captured[state.Session.ID] != 1 {
		t.Fatalf("captured counts=%v err=%v", captured, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	prior := snapshot.Sessions[0].ProjectionFingerprint
	claimed, err := r.ClaimNextMessage(state.Session.ID, time.Now())
	if err != nil || claimed == nil || claimed.ID != queueID {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := r.AckMessage(queueID); err != nil {
		t.Fatal(err)
	}

	stale := `{"id":"queue-update-race","surface":"claude","name":"Queue update","status":"idle","queueCount":1,"open":false,"current":true}`
	_, _, err = r.UpdateCatalogSessionProjection(state.Session.ID, prior, stale)
	var conflict *CatalogQueueProjectionConflict
	if !errors.As(err, &conflict) || conflict.QueueCount != 0 {
		t.Fatalf("expected queue projection conflict at zero, err=%v", err)
	}
	final, err := r.CatalogSnapshot()
	if err != nil || len(final.Sessions) != 1 || final.Sessions[0].ProjectionFingerprint != prior {
		t.Fatalf("stale projection persisted: snapshot=%+v err=%v", final, err)
	}

	correct := `{"id":"queue-update-race","surface":"claude","name":"Queue update","status":"idle","queueCount":0,"open":false,"current":false}`
	if _, created, err := r.UpdateCatalogSessionProjection(state.Session.ID, prior, correct); err != nil || !created {
		t.Fatalf("corrected projection created=%v err=%v", created, err)
	}
}

func TestRecordCatalogSessionRejectsQueueCountCapturedBeforeDrain(t *testing.T) {
	r := openTestRegistry(t)
	state, event := catalogQueueTestState("queue-discovery-race", "Initial", 0)
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		t.Fatal(err)
	}
	queueID, err := r.QueueMessageWithKey(state.Session.ID, "queued", "queue-discovery-race:message")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClaimNextMessage(state.Session.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.AckMessage(queueID); err != nil {
		t.Fatal(err)
	}

	stale, staleEvent := catalogQueueTestState(state.Session.ID, "Stale discovery", 1)
	_, _, err = r.RecordCatalogSession(stale, staleEvent)
	var conflict *CatalogQueueProjectionConflict
	if !errors.As(err, &conflict) || conflict.QueueCount != 0 {
		t.Fatalf("expected discovery queue projection conflict at zero, err=%v", err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 || snapshot.Sessions[0].Session.Name != "Initial" {
		t.Fatalf("stale discovery projection persisted: snapshot=%+v err=%v", snapshot, err)
	}

	correct, correctEvent := catalogQueueTestState(state.Session.ID, "Recovered discovery", 0)
	if _, created, err := r.RecordCatalogSession(correct, correctEvent); err != nil || !created {
		t.Fatalf("corrected discovery created=%v err=%v", created, err)
	}
}
