package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func codexAtItsLimit(node *federationNode, percent float64, reset time.Time) {
	node.fake.mu.Lock()
	defer node.fake.mu.Unlock()
	node.fake.usage = &surface.Usage{FetchedAt: time.Now(), Limits: []surface.UsageWindow{{Kind: "5h", Percent: percent, ResetsAt: reset}}}
}

func TestTheWriteRouteStampsAUsageParkAndPassesEveryOtherBodyThroughUntouched(t *testing.T) {
	mac, _, _ := ledgerOnTwoHosts(t)
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	codexAtItsLimit(mac, 97, reset)
	var seen [][]byte
	route := mac.daemon.ledgerWriteRoute(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, body)
		w.WriteHeader(http.StatusOK)
	})
	post := func(path, body string) []byte {
		t.Helper()
		before := len(seen)
		route(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)))
		if len(seen) != before+1 {
			t.Fatalf("%s %s did not reach the handler", path, body)
		}
		return seen[len(seen)-1]
	}
	for _, write := range []struct{ path, body string }{
		{"/api/v1/ledger/work", `{"projectKey":"` + widgetKey + `",  "sessionId":"local-a","action":"claim","itemId":"widget-W1"}`},
		{"/api/v1/ledger/work", `{"projectKey":"` + widgetKey + `","sessionId":"local-a","action":"park","itemId":"widget-W1","park":{"kind":"later","why":"x"}}`},
		{"/api/v1/ledger/attach", `{"projectKey":"` + widgetKey + `","sessionId":"local-a","as":"idea","park":{"kind":"usage","usage":{"provider":"codex"}}}`},
	} {
		if got := post(write.path, write.body); string(got) != write.body {
			t.Fatalf("%s changed on its way through the route:\n sent %s\n  got %s", write.path, write.body, got)
		}
	}

	stamped := post("/api/v1/ledger/work", `{"projectKey":"`+widgetKey+`","sessionId":"local-a","action":"park","itemId":"widget-W1","reason":"kept","park":{"kind":"usage","why":"out","usage":{"provider":"codex"}}}`)
	var req ledger.WorkRequest
	if err := json.Unmarshal(stamped, &req); err != nil {
		t.Fatal(err)
	}
	if got := req.Park.Usage; got.HostID != mac.daemon.localHost().ID || !got.ResetsAt.Equal(reset) || req.Reason != "kept" || req.Park.Why != "out" {
		t.Fatalf("the usage park the handler got = %s", stamped)
	}

	var added, parked ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "long sweep"}, &added)
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkPark, ItemID: added.Item.ID,
		Park: &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "codex"}}}, &parked)
	if got := parked.Item.Park.Usage; got.HostID != mac.daemon.localHost().ID || !got.ResetsAt.Equal(reset) {
		t.Fatalf("the stored usage park = %+v, want this host %q and reset %s", got, mac.daemon.localHost().ID, reset)
	}
}

func TestAParkForwardedToTheOwnerKeepsTheParkingHostsReading(t *testing.T) {
	mac, owner, record := ledgerOnTwoHosts(t)
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	macReset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	codexAtItsLimit(mac, 98, macReset)
	codexAtItsLimit(owner, 99, time.Now().Add(9*time.Hour).Truncate(time.Second))

	var added, parked ledger.WorkResult
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, Text: "long sweep"}, &added)
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkPark, ItemID: added.Item.ID,
		Park: &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "codex"}}}, &parked)

	here := mac.daemon.localHost().ID
	if here == "" || here == owner.daemon.localHost().ID || record.ID != owner.daemon.localHost().ID {
		t.Fatalf("hosts: parking %q, owner %q, paired record %q", here, owner.daemon.localHost().ID, record.ID)
	}
	board, err := owner.daemon.ledgerStore().Board(widgetKey)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := board.WorkItem(added.Item.ID)
	if !ok || item.State != ledger.WorkParked || item.Park == nil || item.Park.Usage == nil {
		t.Fatalf("the owner holds %+v", item)
	}
	if got := item.Park.Usage; got.HostID != here || !got.ResetsAt.Equal(macReset) {
		t.Fatalf("the owner stored %+v, want the parking host %q and its reset %s", got, here, macReset)
	}
	if got := parked.Item.Park.Usage; got.HostID != here || !got.ResetsAt.Equal(macReset) {
		t.Fatalf("the parking host was answered %+v", got)
	}
}
