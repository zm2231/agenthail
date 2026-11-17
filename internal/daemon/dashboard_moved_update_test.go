package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type movingModelSurface struct {
	*daemonSurface
	moved func()
}

func (s *movingModelSurface) Model(ctx context.Context, session *surface.Session, value string) (string, error) {
	s.moved()
	session.ConfiguredModel = value
	return value, nil
}

func TestAModelChangeToASessionThatMovesDuringItIsAnsweredMoved(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	session := surface.Session{ID: "moving-model", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Transport: "uds"}
	if err := r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	const owner = "h0123456789ab"
	if !registry.ValidHostID(owner) {
		t.Fatalf("%s is not a host id", owner)
	}
	adapter := &movingModelSurface{daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{session.ID: session}, observations: map[string]*surface.TurnObservation{}, accepted: true}}
	adapter.caps.Model = true
	adapter.moved = func() {
		state := registry.CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ProjectionFingerprint: `{"id":"moving-model"}`}
		if _, _, err := r.RecordCatalogSession(state, registry.CatalogEvent{DedupeKey: "session.upserted:moving-model", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if _, err := openSweepDB(t, r.Path()).Exec(`UPDATE sessions SET host_id=? WHERE id=?`, owner, session.ID); err != nil {
			t.Fatal(err)
		}
	}
	d := newTestDaemon(t, r, []surface.Surface{adapter})

	reply := sendAsPerson(d, `{"action":"model","sessionId":"moving-model","model":"opus"}`)
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		MovedToHost string `json:"movedToHost"`
	}
	if reply.Status != http.StatusConflict || json.Unmarshal(reply.Body, &refusal) != nil || refusal.Error.Code != movedCode || refusal.MovedToHost != owner {
		t.Fatalf("a model change to a session that moved while it ran answered %d: %s, want moved to %s", reply.Status, reply.Body, owner)
	}
}
