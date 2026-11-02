package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func ownerOf(t *testing.T, node *federationNode) string {
	t.Helper()
	var owner string
	if err := node.reg.ReadDB().QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key=?`, widgetKey).Scan(&owner); err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
	return owner
}

func assignOwner(t *testing.T, mac *federationNode) (int, ledgerOwnerResponse, string) {
	t.Helper()
	code, body := ledgerRequest(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"})
	var result ledgerOwnerResponse
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &result); err != nil {
			t.Fatal(err)
		}
	}
	return code, result, body
}

func TestALocalFailureAfterTheClaimAbortsIt(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	capturePrompt(t, mac, "local-a", "p-before", "keep this")
	if _, err := mac.reg.WriteDB().Exec(`CREATE TRIGGER inject_owner_failure BEFORE INSERT ON ledger_state WHEN NEW.owner_host_id != '' BEGIN SELECT RAISE(ABORT, 'injected local failure'); END`); err != nil {
		t.Fatal(err)
	}
	if code, _, body := assignOwner(t, mac); code == http.StatusOK || !strings.Contains(body, "injected local failure") {
		t.Fatalf("assignment with a failing local commit: status=%d body=%s", code, body)
	}
	if _, pending := owner.daemon.ledgerClaims.lookup(widgetKey); pending {
		t.Fatal("the owner still holds the claim after the requester aborted it")
	}
	if got := ownerOf(t, owner); got != "" {
		t.Fatalf("the owner took the ledger without a confirm: %q", got)
	}
	if got := ownerOf(t, mac); got != "" {
		t.Fatalf("the requester kept an owner after its commit failed: %q", got)
	}

	if _, err := mac.reg.WriteDB().Exec(`DROP TRIGGER inject_owner_failure`); err != nil {
		t.Fatal(err)
	}
	if code, result, body := assignOwner(t, mac); code != http.StatusOK || !result.Confirmed {
		t.Fatalf("retry after the failure: status=%d body=%s", code, body)
	}
	sameLedgerOnBothHosts(t, mac, owner)
}

func TestALostConfirmStillLandsOnce(t *testing.T) {
	for name, loss := range map[string]int32{"answer lost after applying": confirmApplied, "confirm never arrived": confirmUnsent} {
		t.Run(name, func(t *testing.T) {
			mac, owner, _ := ledgerOnTwoHosts(t)
			before := capturePrompt(t, mac, "local-a", "p-before", "said before the move")
			owner.gate.confirmLoss.Store(loss)
			code, result, body := assignOwner(t, mac)
			if code != http.StatusOK || result.Confirmed || result.Pending == "" {
				t.Fatalf("assignment whose confirm was lost: status=%d body=%s", code, body)
			}
			after := capturePrompt(t, mac, "local-a", "p-after", "said after the move")
			testkit.Eventually(t, "the owner confirms through sync", 20*time.Second, func() bool {
				return ownerOf(t, owner) == owner.daemon.hostIdentity().ID
			})
			sameLedgerOnBothHosts(t, mac, owner)
			for _, ref := range []string{before, after} {
				if n := countEvents(t, owner.reg, ledger.EventUtterance, ref); n != 1 {
					t.Fatalf("%s landed %d times on the owner", ref, n)
				}
			}
		})
	}
}

func restartDaemon(t *testing.T, node *federationNode) {
	t.Helper()
	port := node.server.Listener.Addr().(*net.TCPAddr).Port
	node.stop()
	node.daemon = newTestDaemon(t, node.reg, []surface.Surface{node.fake})
	node.handler = node.daemon.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	node.daemon.discoverCatalog(context.Background())
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	node.serve(t, listener)
}

func TestAnOwnerRestartBetweenClaimAndConfirmStillConverges(t *testing.T) {
	mac, owner, _ := ledgerOnTwoHosts(t)
	before := capturePrompt(t, mac, "local-a", "p-before", "said before the move")
	owner.gate.confirmBlocked.Store(true)
	if code, result, body := assignOwner(t, mac); code != http.StatusOK || result.Confirmed {
		t.Fatalf("assignment with confirms blocked: status=%d body=%s", code, body)
	}
	if _, pending := owner.daemon.ledgerClaims.lookup(widgetKey); !pending {
		t.Fatal("the owner holds no pending claim before the restart")
	}
	restartDaemon(t, owner)
	if _, pending := owner.daemon.ledgerClaims.lookup(widgetKey); pending {
		t.Fatal("the restarted owner still holds the claim")
	}
	if got := ownerOf(t, owner); got != "" {
		t.Fatalf("a pending claim became ownership across the restart: %q", got)
	}
	after := capturePrompt(t, mac, "local-a", "p-after", "said while unconfirmed")
	owner.gate.confirmBlocked.Store(false)
	testkit.Eventually(t, "the restarted owner confirms through sync", 30*time.Second, func() bool {
		return ownerOf(t, owner) == owner.daemon.hostIdentity().ID
	})
	sameLedgerOnBothHosts(t, mac, owner)
	for _, ref := range []string{before, after} {
		if n := countEvents(t, owner.reg, ledger.EventUtterance, ref); n != 1 {
			t.Fatalf("%s landed %d times on the owner", ref, n)
		}
	}
	if got := ownerOf(t, mac); got != owner.daemon.hostIdentity().ID {
		t.Fatalf("the requester's owner is %q", got)
	}
}

func TestALateConfirmTheOwnerRefusesReturnsTheProjectToLocal(t *testing.T) {
	mac, owner, record := ledgerOnTwoHosts(t)
	capturePrompt(t, mac, "local-a", "p-mac", "kept on the mac")
	if _, _, err := ledger.NewStore(owner.reg.WriteDB(), owner.reg.ReadDB()).RecordUtterance(ledger.Utterance{ProjectKey: widgetKey, Ref: "claude-prompt:p-vps", SpeakerID: "remote-a", Text: "kept on the owner", CapturedBy: ledger.CapturedByHook}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).AssignOwner(widgetKey, record.ID); err != nil {
		t.Fatal(err)
	}
	host, ok := mac.daemon.pairedHost(record.ID)
	if !ok {
		t.Fatal("the owner is not paired")
	}
	mac.daemon.wakeLedgerSync(host)
	testkit.Eventually(t, "the requester takes its ledger back", 20*time.Second, func() bool {
		return ownerOf(t, mac) == ""
	})
	if got := ownerOf(t, owner); got != "" {
		t.Fatalf("the owner took a ledger it refused: %q", got)
	}
	capturePrompt(t, mac, "local-a", "p-local", "local again")
}

func TestAPendingClaimBlocksOthersOnlyUntilItExpires(t *testing.T) {
	now := time.Unix(0, 0)
	claims := ledgerClaims{now: func() time.Time { return now }}
	if _, ok := claims.reserve(widgetKey, "mac", true); !ok {
		t.Fatal("first claim refused")
	}
	if holder, ok := claims.reserve(widgetKey, "laptop", false); ok || holder.requester != "mac" {
		t.Fatalf("a second host claimed over a pending claim: %+v", holder)
	}
	if _, ok := claims.reserve(widgetKey, "mac", false); !ok {
		t.Fatal("the same host could not renew its claim")
	}
	now = now.Add(ledgerClaimTTL)
	if _, ok := claims.lookup(widgetKey); ok {
		t.Fatal("an expired claim is still pending")
	}
	if _, ok := claims.reserve(widgetKey, "laptop", false); !ok {
		t.Fatal("an expired claim still blocks another host")
	}
	claims.release(widgetKey, "mac")
	if claim, ok := claims.lookup(widgetKey); !ok || claim.requester != "laptop" {
		t.Fatal("one host released another host's claim")
	}
}
