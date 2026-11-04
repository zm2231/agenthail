package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type retiredHost struct {
	mu       sync.Mutex
	queries  []url.Values
	snapshot map[string]any
}

func (h *retiredHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	w.Header().Set(hosts.ResponseHeader, "1")
	if r.URL.Path == "/api/v1/version" {
		_ = json.NewEncoder(w).Encode(hosts.Version{MinimumProtocol: hosts.ProtocolVersion, MaximumProtocol: hosts.ProtocolVersion, MinimumFederation: hosts.FederationVersion, MaximumFederation: hosts.FederationVersion})
		return
	}
	h.queries = append(h.queries, r.URL.Query())
	_ = json.NewEncoder(w).Encode(h.snapshot)
}

func (h *retiredHost) respond(ids []string, retiredIDs []string, clock int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sessions := []dashboardSession{}
	for _, id := range ids {
		sessions = append(sessions, dashboardSession{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusOffline})
	}
	h.snapshot = map[string]any{"hostEpoch": "epoch", "catalogSeq": 1, "sessions": sessions, "coordinators": []any{}, "retiredClock": clock, "retiredIds": retiredIDs}
}

func (h *retiredHost) lastQuery() url.Values {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.queries[len(h.queries)-1]
}

func TestAReconnectGetsOnlyRetiredRowsItHasNotSeenAndNoticesOnesRemoved(t *testing.T) {
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
	held := func() map[string]bool {
		t.Helper()
		ids, err := r.HostCatalogRowIDs(record.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, id := range ids {
			found[id] = true
		}
		return found
	}

	fake.respond([]string{"live", "ended"}, []string{"ended"}, 7)
	reconcile()
	if fake.lastQuery().Has("retiredAfter") {
		t.Fatal("a first sync must ask for every retired row")
	}
	if got := held(); !got["live"] || !got["ended"] {
		t.Fatalf("held=%v", got)
	}

	fake.respond([]string{"live"}, []string{"ended"}, 9)
	reconcile()
	if query := fake.lastQuery(); query.Get("retiredAfter") != "7" || query.Get("retiredEpoch") != "epoch" {
		t.Fatalf("a reconnect must name what it holds: %v", query)
	}
	if got := held(); !got["ended"] {
		t.Fatalf("a retired row the host did not resend was dropped: %v", got)
	}

	fake.respond([]string{"live"}, []string{}, 11)
	reconcile()
	if query := fake.lastQuery(); query.Get("retiredAfter") != "9" {
		t.Fatalf("query=%v", query)
	}
	if got := held(); got["ended"] {
		t.Fatalf("a row the host removed while away stayed: %v", got)
	}

	fake.respond([]string{"live"}, []string{"never-seen"}, 13)
	reconcile()
	fake.respond([]string{"live", "never-seen"}, []string{"never-seen"}, 15)
	reconcile()
	if fake.lastQuery().Has("retiredAfter") {
		t.Fatal("a daemon missing a retired row must ask for them all again")
	}
	if got := held(); !got["never-seen"] {
		t.Fatalf("held=%v", got)
	}
}
