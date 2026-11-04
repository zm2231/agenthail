package registry

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAStoredWorkspaceReasonNeverMakesALocalRowUnavailable(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "probed", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: "/fixture/probed.jsonl", LastActive: time.Now().Add(-48 * time.Hour)}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), UnavailableReason: "workspace path is unavailable", ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"probed","current":false}`}, CatalogEvent{DedupeKey: "fixture:probed", Type: "session.upserted", EntityID: "probed", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	endedPasses(t, r, 2)
	if !isRetired(t, r, "probed") {
		t.Fatal("the fixture row did not retire")
	}
	current, err := r.ReadCurrentCatalogPresentation(context.Background(), func(string, uint64) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range current.Retired {
		if count.Unavailable {
			t.Fatalf("a retired local row counts as unavailable: %+v", count)
		}
	}
	history, err := r.ReadCatalogPresentation(context.Background(), func(string, uint64) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range history.Rows {
		if row.ID == "probed" && row.UnavailableReason != "" {
			t.Fatalf("a local history row carries %q", row.UnavailableReason)
		}
	}
}
