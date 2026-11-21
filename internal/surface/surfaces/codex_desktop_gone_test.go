package surfaces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASendToADesktopThreadWhileDesktopIsUnreachableWaitsForDesktop(t *testing.T) {
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: "desktop-thread", Originator: codexDesktopOriginator})
	starting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) }))
	t.Cleanup(starting.Close)
	codex := inDaemon(NewCodex(starting.URL))
	codex.managed = true
	session := &surface.Session{ID: "desktop-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Transport: codexTransportDesktop}
	_, err := codex.Send(context.Background(), session, "for Desktop")
	if !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryOutcomeUnknown(err) || session.Transport != codexTransportDesktop {
		t.Fatalf("err=%v transport=%q, want unavailable on the Desktop route", err, session.Transport)
	}
	if fake.Loaded("desktop-thread") || len(fake.Calls("turn/start")) != 0 {
		t.Fatalf("the daemon took a Desktop thread: %v", fake.Methods())
	}
}

func TestACodexListingReportsTheDesktopItRanWithoutOnlyToItsOwnPass(t *testing.T) {
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: "managed-thread", Originator: "agenthail"})
	quit := isolatedManagedRuntime(t)
	passCtx, gap := surface.WithRuntimeGap(context.Background())
	if _, _, err := quit.CatalogList(passCtx); err != nil {
		t.Fatal(err)
	}
	other, otherGap := surface.WithRuntimeGap(context.Background())
	quit.desktopURL = ""
	if _, err := quit.List(other); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(gap.Without(), "Codex Desktop (") {
		t.Fatalf("the pass without Desktop reported %q", gap.Without())
	}
	if otherGap.Without() != "" {
		t.Fatalf("a listing with Desktop out of scope reported %q", otherGap.Without())
	}
}
