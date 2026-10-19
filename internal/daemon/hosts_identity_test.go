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
	if sibling.Code != http.StatusBadRequest || !strings.Contains(sibling.Body.String(), "only that host can send as it") || len(forge.fake.delivered()) != 1 {
		t.Fatalf("a forge session named as the sender from the mac: status=%d body=%s delivered=%v", sibling.Code, sibling.Body.String(), forge.fake.delivered())
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
		{bound, "forge-b", "host_mismatch"},
		{tokenFor(""), hosts.Qualify(macRecord.ID, "mac-a"), "agenthail hosts invite"},
		{bound, hosts.Qualify(macRecord.ID, "mac-a"), ""},
	} {
		response := post(test.token, map[string]any{"action": "send", "sessionId": "forge-a", "sourceSessionId": test.source, "message": "forged"})
		if test.reason == "" {
			if response.Code != http.StatusOK || lastSender(t, forge) != "mac-a" {
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

func TestFederationRelayLivesWithItsSourceAndReachesTheOtherHost(t *testing.T) {
	mac, forge, forgeRecord, macRecord := pairedBothWays(t)
	add := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-add", "fromId": hosts.Qualify(forgeRecord.ID, "forge-a"), "toId": "mac-a"})
	if add.Code != http.StatusOK {
		t.Fatalf("relay-add status=%d body=%s", add.Code, add.Body.String())
	}
	routes, err := forge.reg.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].FromSession != "forge-a" || routes[0].ToSession != "mac-a" {
		t.Fatalf("forge relays=%+v err=%v", routes, err)
	}
	if local, _ := mac.reg.ListRoutes(); len(local) != 0 {
		t.Fatalf("the relay was also stored on the mac: %+v", local)
	}
	listed := mac.request(t, http.MethodGet, "/api/v1/relays?host=forge", nil)
	var page struct {
		Relays []registry.RouteRow `json:"relays"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &page) != nil || len(page.Relays) != 1 {
		t.Fatalf("relays status=%d body=%s", listed.Code, listed.Body.String())
	}
	if page.Relays[0].FromSession != hosts.Qualify(forgeRecord.ID, "forge-a") || page.Relays[0].ToSession != hosts.Qualify(macRecord.ID, "mac-a") {
		t.Fatalf("listed relay=%+v", page.Relays[0])
	}
	outbound := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-add", "fromId": "mac-a", "toId": hosts.Qualify(forgeRecord.ID, "forge-b")})
	if outbound.Code != http.StatusOK {
		t.Fatalf("local relay-add status=%d body=%s", outbound.Code, outbound.Body.String())
	}
	if local, _ := mac.reg.ListRoutes(); len(local) != 1 || local[0].ToSession != "forge-b" {
		t.Fatalf("mac relays=%+v", local)
	}
	remove := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-remove", "relayId": routes[0].ID, "host": "forge"})
	if remove.Code != http.StatusOK {
		t.Fatalf("relay-remove status=%d body=%s", remove.Code, remove.Body.String())
	}
	if left, _ := forge.reg.ListRoutes(); len(left) != 0 {
		t.Fatalf("forge relays after remove=%+v", left)
	}
}

func TestFederationRelayLegKeepsItsHopCountOnTheOtherHost(t *testing.T) {
	mac, forge, _, _ := pairedBothWays(t)
	if _, err := mac.reg.QueueHostSend(registry.HostSend{SessionID: "forge-a", Message: "relayed", Key: "relay:9:turn-1", SourceSessionID: "mac-a", RelayHops: 3}); err != nil {
		t.Fatal(err)
	}
	var item *registry.QueuedMessage
	awaitFederation(t, "the relay leg queued on forge", func() bool {
		claimed, err := forge.reg.ClaimNextMessage("forge-a", time.Now())
		if err == nil && claimed != nil {
			item = claimed
		}
		return item != nil
	})
	if item.RelayHops != 3 || item.Message != "relayed" || item.SourceSessionID != "mac-a" {
		t.Fatalf("queued leg=%+v", item)
	}
	refused := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": "mac-a", "message": "fake relay", "relayHops": 2})
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "relayHops") {
		t.Fatalf("a person's relayHops status=%d body=%s", refused.Code, refused.Body.String())
	}
}

func TestFederationRelayToAHostThatCannotReplyIsRefused(t *testing.T) {
	local, _, record := pairedFederation(t)
	add := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-add", "fromId": hosts.Qualify(record.ID, "remote-a"), "toId": "local-a"})
	if add.Code == http.StatusOK || !strings.Contains(add.Body.String(), "agenthail hosts add") {
		t.Fatalf("one-way relay-add status=%d body=%s", add.Code, add.Body.String())
	}
}

func TestFederationChannelReachesAMemberOnAnotherHost(t *testing.T) {
	mac, forge, forgeRecord, _ := pairedBothWays(t)
	for _, action := range []map[string]any{
		{"action": "channel-create", "channel": "crew"},
		{"action": "channel-add", "channel": "crew", "targetId": "mac-a"},
		{"action": "channel-add", "channel": "crew", "targetId": hosts.Qualify(forgeRecord.ID, "forge-a")},
	} {
		if response := mac.request(t, http.MethodPost, "/api/v1/actions", action); response.Code != http.StatusOK {
			t.Fatalf("%v status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	if members, err := mac.reg.ChannelMembers("crew"); err != nil || len(members) != 2 {
		t.Fatalf("members=%v err=%v", members, err)
	}
	if channels, _ := forge.reg.ListChannels(); len(channels) != 0 {
		t.Fatalf("joining a member made the channel on its host: %+v", channels)
	}
	send := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "crew", "message": "standup", "sourceSessionId": "mac-a"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"queued":1`) {
		t.Fatalf("channel-send status=%d body=%s", send.Code, send.Body.String())
	}
	awaitFederation(t, "the forge member received the channel message", func() bool {
		for _, message := range forge.fake.delivered() {
			if message == "standup" {
				return true
			}
		}
		return false
	})
	if lastSender(t, forge) != "mac-a" {
		t.Fatalf("forge member got it from %v", forge.fake.senders())
	}
}

func TestFederationChannelSendSkipsASenderOnAnotherHost(t *testing.T) {
	mac, _, forgeRecord, _ := pairedBothWays(t)
	for _, action := range []map[string]any{
		{"action": "channel-create", "channel": "crew"},
		{"action": "channel-add", "channel": "crew", "targetId": "mac-a"},
		{"action": "channel-add", "channel": "crew", "targetId": hosts.Qualify(forgeRecord.ID, "forge-a")},
	} {
		if response := mac.request(t, http.MethodPost, "/api/v1/actions", action); response.Code != http.StatusOK {
			t.Fatalf("%v status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	send := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "crew", "message": "standup", "sourceSessionId": hosts.Qualify(forgeRecord.ID, "forge-a")})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"sent":1`) || !strings.Contains(send.Body.String(), `"queued":0`) {
		t.Fatalf("channel-send status=%d body=%s", send.Code, send.Body.String())
	}
}
