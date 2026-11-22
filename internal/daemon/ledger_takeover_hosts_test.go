package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestATakeoverAcrossHostsAsksTheHoldersHostAndFinishesOnTheWatchersMergedLine(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	first := capturePrompt(t, mac, "local-a", "p-1", "build search")
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Search"}, nil)
	mustLedger(t, mac, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: widgetKey, SessionID: "local-a", UtteranceID: first, As: ledger.AsNewAsk, To: "widget-G1", Clauses: []string{"build search"}}, nil)
	for _, text := range []string{"add the index", "wire the endpoint"} {
		mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: text}, nil)
	}
	mustLedger(t, mac, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "local-a", Action: ledger.WorkClaim, ItemID: "widget-W1"}, nil)
	mustLedger(t, owner, "/api/v1/ledger/work", ledger.WorkRequest{ProjectKey: widgetKey, SessionID: "remote-b", Action: ledger.WorkClaim, ItemID: "widget-W2"}, nil)

	url := "https://github.com/example/widget/pull/7"
	typed := map[string]any{"projectKey": widgetKey, "sessionId": "local-a", "action": "log", "itemId": "widget-W1", "text": "PR merged: " + url, "source": ledger.WorkSourcePRWatcher}
	if code, body := ledgerRequest(t, mac, "/api/v1/ledger/work", typed); code == 200 || !strings.Contains(body, "source") {
		t.Fatalf("a typed log claiming the watcher as its source = %d %s", code, body)
	}
	delete(typed, "source")
	mustLedger(t, mac, "/api/v1/ledger/work", typed, nil)
	sources := func() []string {
		t.Helper()
		board, err := ledger.NewStore(owner.reg.WriteDB(), owner.reg.ReadDB()).Board(widgetKey)
		if err != nil {
			t.Fatal(err)
		}
		item, _ := board.WorkItem("widget-W1")
		var out []string
		for _, e := range item.Timeline {
			if e.Kind == "log" {
				out = append(out, e.Source)
			}
		}
		return out
	}
	owner.gate.proxyDown.Store(true)
	prHost{mac.daemon}.LogHeldWork("local-a", "PR merged: "+url)
	if got := sources(); len(got) != 1 {
		t.Fatalf("the owner took the watcher's log while down: sources %q", got)
	}
	pending, err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).PendingReplay(widgetKey, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Type != ledger.EventWorkLog || !strings.Contains(string(pending[0].Payload), ledger.WorkSourcePRWatcher) {
		t.Fatalf("the mac keeps %+v to replay; want the watcher's log, stored", pending)
	}
	owner.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the watcher's log replays to the owner once it is back", 30*time.Second, func() bool {
		return len(sources()) == 2
	})
	if got := sources(); got[0] != "" || got[1] != ledger.WorkSourcePRWatcher {
		t.Fatalf("the owner holds logs from sources %q; want a typed one and the watcher's", got)
	}

	takeover := func(item, then string) (int, string) {
		return ledgerRequest(t, mac, "/api/v1/ledger/work", map[string]any{"projectKey": widgetKey, "sessionId": "lead", "action": "takeover", "itemId": item,
			"reason": "its holder is archived", "takeover": map[string]any{"then": then, "holder": "x", "verdict": "gone"}})
	}
	if err := owner.reg.ArchiveSession("remote-b", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, body := takeover("widget-W2", ledger.TakeoverRelease); code != 200 || !strings.Contains(body, "taken over from remote-b (archived)") {
		t.Fatalf("releasing work whose holder its own host archived, judged from a mirror: %d %s", code, body)
	}
	if code, body := takeover("widget-W1", ledger.TakeoverDone); code == 200 || !strings.Contains(body, "is still running") {
		t.Fatalf("finishing work whose holder runs = %d %s", code, body)
	}
	if err := mac.reg.ArchiveSession("local-a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, body := takeover("widget-W1", ledger.TakeoverDone); code != 200 || !strings.Contains(body, "done by PR merged: "+url) {
		t.Fatalf("finishing merged work whose holder is archived = %d %s", code, body)
	}
}
