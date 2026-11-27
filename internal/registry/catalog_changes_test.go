package registry

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestASessionUpsertNamesTheProjectionKeysItChanged(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusBusy}
	record := func(projection, project string) (changed []string, known bool) {
		t.Helper()
		payload, _ := json.Marshal(map[string]json.RawMessage{"session": json.RawMessage(projection)})
		event, created, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: json.RawMessage(project), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "session.upserted:s1", Type: "session.upserted", EntityID: "s1", Payload: payload})
		if err != nil || !created {
			t.Fatalf("record %s: created=%v err=%v", projection, created, err)
		}
		return SessionUpsertChanges(event)
	}

	if changed, known := record(`{"id":"s1","status":"busy","activity":{"lastProgressAt":"1"}}`, `{}`); known {
		t.Fatalf("a new row has no prior to compare, yet named %v", changed)
	}
	if changed, known := record(`{"id":"s1","status":"busy","activity":{"lastProgressAt":"2"},"lastActive":"2"}`, `{}`); !known || !slices.Equal(changed, []string{"activity", "lastActive"}) {
		t.Fatalf("a progress tick named %v (known=%v), want activity and lastActive", changed, known)
	}
	if changed, known := record(`{"id":"s1","status":"idle"}`, `{}`); !known || !slices.Equal(changed, []string{"activity", "lastActive", "status"}) {
		t.Fatalf("a dropped and a changed key named %v (known=%v)", changed, known)
	}
	if changed, known := record(`{"id":"s1","status":"busy"}`, `{"id":"p2"}`); known {
		t.Fatalf("a move to another project changes more than the projection, yet named only %v", changed)
	}
}

func TestAMirroredUpsertNamesTheProjectionKeysItChanged(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddHost(PairedHost{ID: "h00bb22cc33dd", Name: "b", Endpoint: "https://b.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	apply := func(projection, unavailable string) (changed []string, known bool) {
		t.Helper()
		event, created, err := r.ApplyHostCatalogRow("h00bb22cc33dd", HostCatalogRow{Session: surface.Session{ID: "h00bb22cc33dd::s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusBusy}, EntityID: "h00bb22cc33dd::s1", Projection: json.RawMessage(projection), UnavailableReason: unavailable})
		if err != nil || !created {
			t.Fatalf("apply %s: created=%v err=%v", projection, created, err)
		}
		return SessionUpsertChanges(event)
	}

	if changed, known := apply(`{"id":"h00bb22cc33dd::s1","status":"busy","activity":{"lastProgressAt":"1"}}`, ""); known {
		t.Fatalf("a first mirrored row has no prior to compare, yet named %v", changed)
	}
	if changed, known := apply(`{"id":"h00bb22cc33dd::s1","status":"busy","activity":{"lastProgressAt":"2"}}`, ""); !known || !slices.Equal(changed, []string{"activity"}) {
		t.Fatalf("a mirrored progress tick named %v (known=%v), want activity", changed, known)
	}
	if changed, known := apply(`{"id":"h00bb22cc33dd::s1","status":"busy","activity":{"lastProgressAt":"3"}}`, "host is offline"); known {
		t.Fatalf("a mirrored row going unavailable changes more than the projection, yet named only %v", changed)
	}
}
