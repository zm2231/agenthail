package daemon

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnImplicitProjectsCapturedWordsCanBeAttached(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fake := &daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	handler := d.dashboardHandler(&dashboardServer{token: "test-token"})

	repo := widgetRepo(t, "https://github.com/example/implicit.git")
	session := surface.Session{ID: "implicit-s1", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "work", Cwd: repo, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", LastActive: time.Now()}
	fake.sessions[session.ID] = session
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	const key = "repo:github.com/example/implicit"
	if records, err := r.ListProjects(); err != nil || len(records) != 0 {
		t.Fatalf("the fixture must have no project rows: %+v %v", records, err)
	}
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable(key); err != nil {
		t.Fatal(err)
	}
	resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: session.ID, PromptID: "p-implicit", Prompt: "keep the importer"})
	if !resp.Captured || resp.ProjectKey != key {
		t.Fatalf("capture in the implicit project: %+v", resp)
	}
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: key, SessionID: session.ID, UtteranceID: resp.Ref, As: ledger.AsConversation}, nil); code != http.StatusOK {
		t.Fatalf("attach in the implicit project: %d %s", code, body)
	}
	if code, _ := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: "repo:github.com/example/nowhere", SessionID: session.ID, UtteranceID: resp.Ref, As: ledger.AsConversation}, nil); code != http.StatusNotFound {
		t.Fatalf("a key no catalog project has was accepted: %d", code)
	}
}
