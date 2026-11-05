package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/testkit"
)

func syncViewOf(t *testing.T, node *federationNode) ledgerSyncView {
	t.Helper()
	response := node.request(t, http.MethodGet, "/api/v1/ledger/sync/status", nil)
	var views []ledgerSyncView
	if err := json.Unmarshal(response.Body.Bytes(), &views); err != nil || len(views) != 1 {
		t.Fatalf("sync status %d: %s (%v)", response.Code, response.Body.String(), err)
	}
	return views[0]
}

func TestTheReplicaChecksItsLogAgainstTheOwnersAndFindsTheFirstDifference(t *testing.T) {
	mac, owner, record := ledgerOnTwoHosts(t)
	capturePrompt(t, mac, "local-a", "p-one", "first words")
	mustLedger(t, mac, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "local-a", Name: "Search"}, nil)
	capturePrompt(t, mac, "local-a", "p-two", "second words")
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	sameLedgerOnBothHosts(t, widgetKey, mac, owner)
	testkit.Eventually(t, "the replica reports a matching log", 20*time.Second, func() bool {
		view := syncViewOf(t, mac)
		return view.Log == "match" && view.LogThrough == view.Cursor && view.Cursor > 0
	})

	var changed int64
	if err := mac.reg.ReadDB().QueryRow(`SELECT owner_seq FROM ledger_events WHERE project_key=? AND owner_seq>0 ORDER BY owner_seq LIMIT 1 OFFSET 1`, widgetKey).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if _, err := mac.reg.WriteDB().Exec(`UPDATE ledger_events SET payload=CAST(payload AS TEXT)||' ' WHERE project_key=? AND owner_seq=?`, widgetKey, changed); err != nil {
		t.Fatal(err)
	}
	host, ok := mac.daemon.pairedHost(record.ID)
	if !ok {
		t.Fatal("the owner is not paired")
	}
	mac.daemon.wakeLedgerSync(host)
	testkit.Eventually(t, "the replica reports where its log first differs", 20*time.Second, func() bool {
		view := syncViewOf(t, mac)
		return view.Log == "mismatch" && view.FirstDiff == changed
	})

	searched := owner.gate.digestRequests.Load()
	mustLedger(t, owner, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widgetKey, SessionID: "remote-a", Name: "Infra"}, nil)
	before := syncViewOf(t, mac).LogThrough
	mac.daemon.wakeLedgerSync(host)
	testkit.Eventually(t, "the replica checks again past the owner's new event", 20*time.Second, func() bool {
		view := syncViewOf(t, mac)
		return view.LogThrough > before && view.Log == "mismatch"
	})
	if view := syncViewOf(t, mac); view.FirstDiff != changed {
		t.Fatalf("the first difference moved to %d after the owner appended", view.FirstDiff)
	}
	if again := owner.gate.digestRequests.Load(); again != searched {
		t.Fatalf("an append on the owner re-ran the search: %d more digest lookups", again-searched)
	}
}

func TestALogLongerThanOnePageIsCheckedThroughItsEnd(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	store := ledger.NewStore(owner.reg.WriteDB(), owner.reg.ReadDB())
	for i := range ledgerPullPage + 100 {
		if _, _, err := store.RecordUtterance(ledger.Utterance{ProjectKey: widgetKey, Ref: fmt.Sprintf("claude-prompt:owner-%d", i), SpeakerID: "remote-a", Text: "said on the owner", CapturedBy: ledger.CapturedByHook}); err != nil {
			t.Fatal(err)
		}
	}
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
	testkit.Eventually(t, "the replica checks the whole pulled log", 30*time.Second, func() bool {
		head, err := store.LatestSeq(widgetKey)
		if err != nil {
			t.Fatal(err)
		}
		view := syncViewOf(t, mac)
		return head > int64(ledgerPullPage) && view.Log == "match" && view.LogThrough == head
	})
}

func TestAPageWithoutADigestIsAFailedCheck(t *testing.T) {
	mac, _, record := ledgerOnTwoHosts(t)
	host, ok := mac.daemon.pairedHost(record.ID)
	if !ok {
		t.Fatal("the owner is not paired")
	}
	if check := mac.daemon.checkLedgerLog(context.Background(), host, widgetKey, nil); check.Error == "" || check.Match {
		t.Fatalf("a pull without a digest passed the check: %+v", check)
	}
}
