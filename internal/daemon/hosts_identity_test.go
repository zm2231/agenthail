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
	laptop := newFederationNode(t, "laptop-a")
	vps := newFederationNode(t, "vps-a", "vps-b")
	laptop.serve(t, nil)
	vps.serve(t, nil)
	roots := testRoots(laptop.server)
	laptop.start(t, roots)
	vps.start(t, roots)
	vpsRecord := laptop.pair(t, vps, "vps")
	laptopRecord := vps.pair(t, laptop, "laptop")
	awaitFederation(t, "both mirrors", func() bool {
		return len(sessionsOf(laptop.snapshot(t), vpsRecord.ID)) == 2 && len(sessionsOf(vps.snapshot(t), laptopRecord.ID)) == 1
	})
	return laptop, vps, vpsRecord, laptopRecord
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
	laptop, vps, vpsRecord, laptopRecord := pairedBothWays(t)
	send := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(vpsRecord.ID, "vps-a"), "sourceSessionId": "laptop-a", "message": "from the laptop"})
	if send.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	if got := vps.fake.delivered(); len(got) != 1 || got[0] != "from the laptop" {
		t.Fatalf("vps received %v", got)
	}
	if sender := lastSender(t, vps); sender != "laptop-a" {
		t.Fatalf("vps delivered from %q, want its mirror of laptop-a", sender)
	}
	if owner, err := vps.reg.SessionHost("laptop-a"); err != nil || owner != laptopRecord.ID {
		t.Fatalf("vps owner of laptop-a=%q err=%v", owner, err)
	}
	reply := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(laptopRecord.ID, "laptop-a"), "sourceSessionId": "vps-a", "message": "reply"})
	if reply.Code != http.StatusOK {
		t.Fatalf("reply status=%d body=%s", reply.Code, reply.Body.String())
	}
	if got := laptop.fake.delivered(); len(got) != 1 || got[0] != "reply" || lastSender(t, laptop) != "vps-a" {
		t.Fatalf("laptop received %v from %v", got, laptop.fake.senders())
	}
	sibling := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(vpsRecord.ID, "vps-a"), "sourceSessionId": hosts.Qualify(vpsRecord.ID, "vps-b"), "message": "between vps sessions"})
	if sibling.Code != http.StatusBadRequest || !strings.Contains(sibling.Body.String(), "only that host can send as it") || len(vps.fake.delivered()) != 1 {
		t.Fatalf("a vps session named as the sender from the laptop: status=%d body=%s delivered=%v", sibling.Code, sibling.Body.String(), vps.fake.delivered())
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
	_, vps, _, laptopRecord := pairedBothWays(t)
	tokenFor := func(hostID string) string {
		pairing, err := vps.reg.CreateDevicePairing("peer", []string{"read", "control", registry.HostScope}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := vps.reg.CompleteDevicePairing(pairing.Secret, "laptop", hostID)
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
		vps.handler.ServeHTTP(response, request)
		return response
	}
	bound := tokenFor(laptopRecord.ID)
	for _, test := range []struct {
		token, source, reason string
	}{
		{bound, "h0000000000ff::elsewhere", "host_mismatch"},
		{bound, "vps-b", "host_mismatch"},
		{tokenFor(""), hosts.Qualify(laptopRecord.ID, "laptop-a"), "agenthail hosts invite"},
		{bound, hosts.Qualify(laptopRecord.ID, "laptop-a"), ""},
	} {
		response := post(test.token, map[string]any{"action": "send", "sessionId": "vps-a", "sourceSessionId": test.source, "message": "forged"})
		if test.reason == "" {
			if response.Code != http.StatusOK || lastSender(t, vps) != "laptop-a" {
				t.Fatalf("own-session sender status=%d body=%s", response.Code, response.Body.String())
			}
			continue
		}
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.reason) {
			t.Fatalf("source %s status=%d body=%s", test.source, response.Code, response.Body.String())
		}
	}
	if got := vps.fake.delivered(); len(got) != 1 {
		t.Fatalf("vps delivered %v; only the host's own-session sender may pass", got)
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
	laptop, vps, vpsRecord, laptopRecord := pairedBothWays(t)
	add := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-add", "fromId": hosts.Qualify(vpsRecord.ID, "vps-a"), "toId": "laptop-a"})
	if add.Code != http.StatusOK {
		t.Fatalf("relay-add status=%d body=%s", add.Code, add.Body.String())
	}
	routes, err := vps.reg.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].FromSession != "vps-a" || routes[0].ToSession != "laptop-a" {
		t.Fatalf("vps relays=%+v err=%v", routes, err)
	}
	if local, _ := laptop.reg.ListRoutes(); len(local) != 0 {
		t.Fatalf("the relay was also stored on the laptop: %+v", local)
	}
	listed := laptop.request(t, http.MethodGet, "/api/v1/relays?host=vps", nil)
	var page struct {
		Relays []registry.RouteRow `json:"relays"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &page) != nil || len(page.Relays) != 1 {
		t.Fatalf("relays status=%d body=%s", listed.Code, listed.Body.String())
	}
	if page.Relays[0].FromSession != hosts.Qualify(vpsRecord.ID, "vps-a") || page.Relays[0].ToSession != hosts.Qualify(laptopRecord.ID, "laptop-a") {
		t.Fatalf("listed relay=%+v", page.Relays[0])
	}
	outbound := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-add", "fromId": "laptop-a", "toId": hosts.Qualify(vpsRecord.ID, "vps-b")})
	if outbound.Code != http.StatusOK {
		t.Fatalf("local relay-add status=%d body=%s", outbound.Code, outbound.Body.String())
	}
	if local, _ := laptop.reg.ListRoutes(); len(local) != 1 || local[0].ToSession != "vps-b" {
		t.Fatalf("laptop relays=%+v", local)
	}
	remove := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-remove", "relayId": routes[0].ID, "host": "vps"})
	if remove.Code != http.StatusOK {
		t.Fatalf("relay-remove status=%d body=%s", remove.Code, remove.Body.String())
	}
	if left, _ := vps.reg.ListRoutes(); len(left) != 0 {
		t.Fatalf("vps relays after remove=%+v", left)
	}
}

func TestFederationRelayLegKeepsItsHopCountOnTheOtherHost(t *testing.T) {
	laptop, vps, _, _ := pairedBothWays(t)
	if _, err := laptop.reg.QueueHostSend(registry.HostSend{SessionID: "vps-a", Message: "relayed", Key: "relay:9:turn-1", SourceSessionID: "laptop-a", RelayHops: 3}); err != nil {
		t.Fatal(err)
	}
	var item *registry.QueuedMessage
	awaitFederation(t, "the relay leg queued on vps", func() bool {
		claimed, err := vps.reg.ClaimNextMessage("vps-a", time.Now())
		if err == nil && claimed != nil {
			item = claimed
		}
		return item != nil
	})
	if item.RelayHops != 3 || item.Message != "relayed" || item.SourceSessionID != "laptop-a" {
		t.Fatalf("queued leg=%+v", item)
	}
	refused := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": "laptop-a", "message": "fake relay", "relayHops": 2})
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
	laptop, vps, vpsRecord, _ := pairedBothWays(t)
	for _, action := range []map[string]any{
		{"action": "channel-create", "channel": "crew"},
		{"action": "channel-add", "channel": "crew", "targetId": "laptop-a"},
		{"action": "channel-add", "channel": "crew", "targetId": hosts.Qualify(vpsRecord.ID, "vps-a")},
	} {
		if response := laptop.request(t, http.MethodPost, "/api/v1/actions", action); response.Code != http.StatusOK {
			t.Fatalf("%v status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	if members, err := laptop.reg.ChannelMembers("crew"); err != nil || len(members) != 2 {
		t.Fatalf("members=%v err=%v", members, err)
	}
	if channels, _ := vps.reg.ListChannels(); len(channels) != 0 {
		t.Fatalf("joining a member made the channel on its host: %+v", channels)
	}
	send := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "crew", "message": "standup", "sourceSessionId": "laptop-a"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"queued":1`) {
		t.Fatalf("channel-send status=%d body=%s", send.Code, send.Body.String())
	}
	awaitFederation(t, "the vps member received the channel message", func() bool {
		for _, message := range vps.fake.delivered() {
			if strings.HasPrefix(message, "[from ") && strings.HasSuffix(message, " via #crew] standup") {
				return true
			}
		}
		return false
	})
	if lastSender(t, vps) != "laptop-a" {
		t.Fatalf("vps member got it from %v", vps.fake.senders())
	}
}

func TestFederationChannelSendSkipsASenderOnAnotherHost(t *testing.T) {
	laptop, _, vpsRecord, _ := pairedBothWays(t)
	for _, action := range []map[string]any{
		{"action": "channel-create", "channel": "crew"},
		{"action": "channel-add", "channel": "crew", "targetId": "laptop-a"},
		{"action": "channel-add", "channel": "crew", "targetId": hosts.Qualify(vpsRecord.ID, "vps-a")},
	} {
		if response := laptop.request(t, http.MethodPost, "/api/v1/actions", action); response.Code != http.StatusOK {
			t.Fatalf("%v status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	send := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "crew", "message": "standup", "sourceSessionId": hosts.Qualify(vpsRecord.ID, "vps-a")})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"sent":1`) || !strings.Contains(send.Body.String(), `"queued":0`) {
		t.Fatalf("channel-send status=%d body=%s", send.Code, send.Body.String())
	}
}
