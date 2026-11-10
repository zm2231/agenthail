package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAHostTellsItsPeersWhichSessionsEnded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, _, _, _ := daemonFixture(t)
	for _, session := range []surface.Session{
		{ID: "ended", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusNotLoaded, Ended: true, Source: "vscode", Transport: "desktop", LastActive: time.Now().Add(-48 * time.Hour)},
		{ID: "held", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", LastActive: time.Now().Add(-48 * time.Hour)},
	} {
		projection := `{"id":"` + session.ID + `"}`
		if _, _, err := r.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: projection}, registry.CatalogEvent{DedupeKey: "fixture:" + session.ID, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.WithValue(context.Background(), apiHostPrincipalContextKey{}, apiHostPrincipal{host: true, hostID: "h0123456789ab", hostName: "peer"})
	state, err := d.dashboardState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var received remoteSnapshot
	if err := json.Unmarshal(encoded, &received); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"ended": true, "held": false}
	for _, row := range received.Sessions {
		ended, listed := want[row.ID]
		if !listed {
			continue
		}
		delete(want, row.ID)
		if row.Ended != ended {
			t.Errorf("a peer reads %s as ended=%v, want %v", row.ID, row.Ended, ended)
		}
	}
	if len(want) != 0 {
		t.Fatalf("the snapshot a peer reads left out %v", want)
	}
}

func TestAHostsRetiredListRepairsAStaleCopyOnItsPeer(t *testing.T) {
	allowLoopbackHosts(t)
	t.Setenv("HOME", t.TempDir())
	d, r, _, _, _ := daemonFixture(t)
	fake := &retiredHost{}
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	record, err := r.AddHost(registry.PairedHost{ID: "h0123456789ab", Name: "peer", Endpoint: server.URL, Address: "127.0.0.1", Token: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	client, err := hosts.NewClient(server.URL, "127.0.0.1", "synthetic", hosts.ClientOptions{RootCAs: testRoots(server)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	host := &pairedHost{record: record, client: client}
	reconcile := func() {
		t.Helper()
		if _, err := d.reconcileHost(context.Background(), host); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	stale := dashboardSession{ID: "stale", Name: "stale", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusNotLoaded, LastActive: old}
	live := dashboardSession{ID: "live", Name: "live", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: old}
	fake.mu.Lock()
	fake.snapshot = map[string]any{"hostEpoch": "epoch", "catalogSeq": 1, "sessions": []dashboardSession{stale, live}, "coordinators": []any{}, "retiredClock": 7, "retiredIds": []string{}}
	fake.mu.Unlock()
	reconcile()
	if session, err := r.Session("stale"); err != nil || session.Ended {
		t.Fatalf("fixture: the first copy should be unended: %+v %v", session, err)
	}
	shownEnded := func(when string, want bool) {
		t.Helper()
		page, err := d.dashboardState(context.Background(), registry.CatalogPageRequest{Scope: "all", Limit: 50, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for read, rows := range map[string][]dashboardSession{"the all page": page.Sessions, "the session view": view.Sessions} {
			found := false
			for _, row := range rows {
				if strings.HasSuffix(row.ID, "stale") {
					found = true
					if row.Ended != want {
						t.Errorf("%s shows the host's retired row as ended=%v %s, want %v", read, row.Ended, when, want)
					}
				}
			}
			if !found {
				t.Errorf("%s does not show the host's row %s", read, when)
			}
		}
	}
	shownEnded("before its host retired it", false)

	fake.mu.Lock()
	fake.snapshot = map[string]any{"hostEpoch": "epoch", "catalogSeq": 2, "sessions": []dashboardSession{live}, "coordinators": []any{}, "retiredClock": 9, "retiredIds": []string{"stale"}}
	fake.mu.Unlock()
	reconcile()
	if session, err := r.Session("stale"); err != nil || !session.Ended {
		t.Fatalf("a row its host retired is not ended on the peer: %+v %v", session, err)
	}
	unretired, err := r.HostUnretiredCatalogRows(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range unretired {
		if row.Session.ID == "stale" {
			t.Fatal("a row its host retired stays on the peer's current reads")
		}
	}
	shownEnded("after its host retired it", true)
	if session, err := r.Session("live"); err != nil || session.Ended {
		t.Fatalf("a row its host still serves was ended: %+v %v", session, err)
	}
}
