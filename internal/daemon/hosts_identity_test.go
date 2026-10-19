package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func pairedBothWays(t *testing.T) (*federationNode, *federationNode, registry.PairedHost, registry.PairedHost) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	mac := newFederationNode(t, "mac-a")
	forge := newFederationNode(t, "forge-a", "forge-b")
	mac.serve(t, nil)
	forge.serve(t, nil)
	roots := testRoots(mac.server)
	mac.start(t, roots)
	forge.start(t, roots)
	forgeRecord := mac.pair(t, forge, "forge")
	macRecord := forge.pair(t, mac, "mac")
	awaitFederation(t, "both mirrors", func() bool {
		return len(sessionsOf(mac.snapshot(t), forgeRecord.ID)) == 2 && len(sessionsOf(forge.snapshot(t), macRecord.ID)) == 1
	})
	return mac, forge, forgeRecord, macRecord
}

func lastSender(t *testing.T, node *federationNode) string {
	t.Helper()
	senders := node.fake.senders()
	if len(senders) == 0 {
		t.Fatal("nothing was delivered")
	}
	return senders[len(senders)-1]
}

func TestFederationMessageCarriesItsSenderAndTheReplyComesBack(t *testing.T) {
	mac, forge, forgeRecord, macRecord := pairedBothWays(t)
	send := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(forgeRecord.ID, "forge-a"), "sourceSessionId": "mac-a", "message": "from the mac"})
	if send.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	if got := forge.fake.delivered(); len(got) != 1 || got[0] != "from the mac" {
		t.Fatalf("forge received %v", got)
	}
	if sender := lastSender(t, forge); sender != "mac-a" {
		t.Fatalf("forge delivered from %q, want its mirror of mac-a", sender)
	}
	if owner, err := forge.reg.SessionHost("mac-a"); err != nil || owner != macRecord.ID {
		t.Fatalf("forge owner of mac-a=%q err=%v", owner, err)
	}
	reply := forge.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(macRecord.ID, "mac-a"), "sourceSessionId": "forge-a", "message": "reply"})
	if reply.Code != http.StatusOK {
		t.Fatalf("reply status=%d body=%s", reply.Code, reply.Body.String())
	}
	if got := mac.fake.delivered(); len(got) != 1 || got[0] != "reply" || lastSender(t, mac) != "forge-a" {
		t.Fatalf("mac received %v from %v", got, mac.fake.senders())
	}
	sibling := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(forgeRecord.ID, "forge-a"), "sourceSessionId": hosts.Qualify(forgeRecord.ID, "forge-b"), "message": "between forge sessions"})
	if sibling.Code != http.StatusOK || lastSender(t, forge) != "forge-b" {
		t.Fatalf("sibling status=%d body=%s senders=%v", sibling.Code, sibling.Body.String(), forge.fake.senders())
	}
}

func TestFederationOneWayPairingDeliversWithNoReplyAddress(t *testing.T) {
	local, remote, record := pairedFederation(t)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(record.ID, "remote-a"), "sourceSessionId": "local-a", "message": "one way"})
	if send.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	if got := remote.fake.delivered(); len(got) != 1 || got[0] != "one way" {
		t.Fatalf("remote received %v", got)
	}
	localIdentity, _ := local.reg.HostIdentity()
	sender := lastSender(t, remote)
	if sender != "unrepliable:"+localIdentity.ID+":local-a" {
		t.Fatalf("sender=%q", sender)
	}
	session, err := remote.reg.Session(sender)
	if err != nil {
		t.Fatal(err)
	}
	if want := localIdentity.Name + "/work local-a (no reply address)"; session.Name != want {
		t.Fatalf("sender name=%q, want %q", session.Name, want)
	}
	for _, row := range remote.snapshot(t).Sessions {
		if strings.Contains(row.ID, "unrepliable") {
			t.Fatalf("the sender became a listed session: %+v", row)
		}
	}
}

func TestFederationTrustsASenderOnlyFromItsOwnHost(t *testing.T) {
	_, forge, _, macRecord := pairedBothWays(t)
	tokenFor := func(hostID string) string {
		pairing, err := forge.reg.CreateDevicePairing("peer", []string{"read", "control", registry.HostScope}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := forge.reg.CompleteDevicePairing(pairing.Secret, "mac", hostID)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	post := func(token string, body map[string]any) *httptest.ResponseRecorder {
		encoded, _ := json.Marshal(body)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(encoded))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Idempotency-Key", "test-"+token[len(token)-8:]+body["sourceSessionId"].(string))
		response := httptest.NewRecorder()
		forge.handler.ServeHTTP(response, request)
		return response
	}
	bound := tokenFor(macRecord.ID)
	for _, test := range []struct {
		token, source, reason string
	}{
		{bound, "h0000000000ff::elsewhere", "host_mismatch"},
		{bound, "forge-b", ""},
		{tokenFor(""), hosts.Qualify(macRecord.ID, "mac-a"), "agenthail hosts invite"},
	} {
		response := post(test.token, map[string]any{"action": "send", "sessionId": "forge-a", "sourceSessionId": test.source, "message": "forged"})
		if test.reason == "" {
			if response.Code != http.StatusOK || lastSender(t, forge) != "forge-b" {
				t.Fatalf("own-session sender status=%d body=%s", response.Code, response.Body.String())
			}
			continue
		}
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.reason) {
			t.Fatalf("source %s status=%d body=%s", test.source, response.Code, response.Body.String())
		}
	}
	if got := forge.fake.delivered(); len(got) != 1 {
		t.Fatalf("forge delivered %v; only the host's own-session sender may pass", got)
	}
}

func TestFederationOffersAConnectedHostsSessionsAsPeers(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	advertised := func() map[string]bool {
		seen := map[string]bool{}
		local.daemon.registerRecentClaudePeers(context.Background(), func(_ context.Context, sessions []surface.Session) map[string]error {
			for _, session := range sessions {
				seen[session.ID] = true
			}
			return nil
		})
		return seen
	}
	if seen := advertised(); !seen["local-a"] || !seen["remote-a"] || !seen["remote-b"] {
		t.Fatalf("advertised %v, want the local session and both of the host's", seen)
	}
	remote.stop()
	awaitFederation(t, "the host is no longer offered", func() bool {
		seen := advertised()
		return seen["local-a"] && !seen["remote-a"] && !seen["remote-b"]
	})
}
