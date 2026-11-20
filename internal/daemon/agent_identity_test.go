package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/agentinbox"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func withInbox(node *federationNode) {
	reg := node.reg
	node.daemon.Surfaces = append(node.daemon.Surfaces, agentinbox.New(func() *registry.Registry { return reg }))
}

func identityID(t *testing.T, node *federationNode, name string) string {
	t.Helper()
	host, err := node.reg.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return surface.AgentIdentityID(host.ID, name)
}

// pairedWithIdentity pairs a laptop and a box both ways. Each holds its own
// identity named scout, and each daemon serves identities.
func pairedWithIdentity(t *testing.T) (laptop, box *federationNode, boxRecord, laptopRecord registry.PairedHost) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	laptop = newFederationNode(t, "laptop-a")
	box = newFederationNode(t, "box-a")
	for _, node := range []*federationNode{laptop, box} {
		withInbox(node)
		if _, err := node.reg.AddAgentIdentity("scout"); err != nil {
			t.Fatal(err)
		}
		node.daemon.discoverCatalog(context.Background())
	}
	laptop.serve(t, nil)
	box.serve(t, nil)
	roots := testRoots(laptop.server)
	laptop.start(t, roots)
	box.start(t, roots)
	boxRecord = laptop.pair(t, box, "box")
	laptopRecord = box.pair(t, laptop, "laptop")
	testkit.Eventually(t, "each scout mirrored on the other host, and the laptop's session on the box", 10*time.Second, func() bool {
		owner, err := laptop.reg.SessionHost(identityID(t, box, "scout"))
		mirrored, mirroredErr := box.reg.SessionHost(identityID(t, laptop, "scout"))
		back, backErr := box.reg.SessionHost("laptop-a")
		return err == nil && owner == boxRecord.ID && mirroredErr == nil && mirrored == laptopRecord.ID && backErr == nil && back == laptopRecord.ID
	})
	return laptop, box, boxRecord, laptopRecord
}

func inbox(t *testing.T, node *federationNode, name string) []registry.InboxMessage {
	t.Helper()
	messages, err := node.reg.ReadInbox(name, registry.InboxQuery{All: true, Peek: true})
	if err != nil {
		t.Fatal(err)
	}
	return messages
}

func TestAnAgentIdentitySendsAcrossHostsAndItsRepliesLandInItsInbox(t *testing.T) {
	laptop, box, boxRecord, laptopRecord := pairedWithIdentity(t)
	scout := identityID(t, box, "scout")
	laptopScout := identityID(t, laptop, "scout")

	for _, node := range []*federationNode{laptop, box} {
		for _, row := range node.snapshot(t).Sessions {
			if strings.Contains(row.ID, scout) {
				t.Fatalf("an identity is listed as a session: %+v", row)
			}
		}
		for _, entry := range node.snapshot(t).Surfaces {
			if entry.Name == string(surface.KindAgent) {
				t.Fatalf("the identity surface is offered to clients: %+v", entry)
			}
		}
	}

	out := box.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(laptopRecord.ID, "laptop-a"), "sourceSessionId": scout, "message": "from scout"})
	if out.Code != http.StatusOK {
		t.Fatalf("scout's send status=%d body=%s", out.Code, out.Body.String())
	}
	if got := laptop.fake.delivered(); len(got) != 1 || !strings.Contains(got[0], "from scout") {
		t.Fatalf("laptop received %v", got)
	}
	if sender := lastSender(t, laptop); sender != scout {
		t.Fatalf("laptop delivered from %q, want its mirror of scout", sender)
	}
	if target, err := laptop.reg.ReplyTarget(scout); err != nil || target != "box::@scout" {
		t.Fatalf("the laptop addresses scout as %q (err %v), want box::@scout", target, err)
	}

	resolved := laptop.request(t, http.MethodGet, "/api/v1/resolve?scope=remote&host=box&target=@scout", nil)
	var answer resolveResponse
	if err := json.Unmarshal(resolved.Body.Bytes(), &answer); err != nil || len(answer.Matches) != 1 || answer.Matches[0].ID != hosts.Qualify(boxRecord.ID, scout) || !answer.Matches[0].Exact || answer.Matches[0].Surface != surface.KindAgent {
		t.Fatalf("box::@scout resolves to status=%d body=%s", resolved.Code, resolved.Body.String())
	}

	reply := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": answer.Matches[0].ID, "sourceSessionId": "laptop-a", "message": "reply to scout"})
	if reply.Code != http.StatusOK || !strings.Contains(reply.Body.String(), "inbox of @scout") {
		t.Fatalf("reply status=%d body=%s", reply.Code, reply.Body.String())
	}
	messages := inbox(t, box, "scout")
	if len(messages) != 1 || !strings.Contains(messages[0].Message, "reply to scout") || messages[0].SenderID != "laptop-a" || messages[0].SenderName != "laptop::laptop-a" {
		t.Fatalf("scout's inbox=%+v", messages)
	}
	if got := box.fake.delivered(); len(got) != 0 {
		t.Fatalf("a model on the box got scout's message: %v", got)
	}
	if state, found, err := box.reg.RuntimeState(scout); err != nil || found && (state.ActiveTurnID != "" || state.LastStatus == surface.StatusBusy || state.NotificationArmed) {
		t.Fatalf("an inbox delivery recorded a turn: state=%+v found=%v err=%v", state, found, err)
	}

	other := box.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(laptopRecord.ID, laptopScout), "sourceSessionId": "box-a", "message": "for the laptop's scout"})
	if other.Code != http.StatusOK {
		t.Fatalf("send to the laptop's scout status=%d body=%s", other.Code, other.Body.String())
	}
	if got := inbox(t, laptop, "scout"); len(got) != 1 || !strings.Contains(got[0].Message, "for the laptop's scout") || got[0].SenderName != "box::box-a" {
		t.Fatalf("the laptop's scout inbox=%+v", got)
	}
	if got := inbox(t, box, "scout"); len(got) != 1 {
		t.Fatalf("the box's scout got the laptop's message: %+v", got)
	}

	local := box.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": scout, "sourceSessionId": "box-a", "message": "local note"})
	if local.Code != http.StatusOK {
		t.Fatalf("local send status=%d body=%s", local.Code, local.Body.String())
	}
	if messages := inbox(t, box, "scout"); len(messages) != 2 || messages[1].SenderName != "box-a" {
		t.Fatalf("scout's inbox after a local send=%+v", messages)
	}
}

func TestAQueuedMessageToAnIdentityLandsInItsInboxWithoutATurn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	node := newFederationNode(t, "box-a")
	withInbox(node)
	if _, err := node.reg.AddAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	scout := identityID(t, node, "scout")
	if err := node.reg.QueueMessage(scout, "a notice that waited"); err != nil {
		t.Fatal(err)
	}
	session, err := node.reg.Session(scout)
	if err != nil {
		t.Fatal(err)
	}
	adapter := node.daemon.Surfaces[len(node.daemon.Surfaces)-1]
	node.daemon.drainMessageQueue(context.Background(), adapter, session)
	if got := inbox(t, node, "scout"); len(got) != 1 || got[0].Message != "a notice that waited" {
		t.Fatalf("inbox=%+v", got)
	}
	if state, found, err := node.reg.RuntimeState(scout); err != nil || found && (state.ActiveTurnID != "" || state.LastStatus == surface.StatusBusy || state.NotificationArmed) {
		t.Fatalf("a queued inbox delivery recorded a turn: state=%+v found=%v err=%v", state, found, err)
	}
	if count := node.reg.QueueCount(scout); count != 0 {
		t.Fatalf("queue still holds %d", count)
	}
}

func TestAQuietIdentityStaysAClaudePeer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	node := newFederationNode(t, "box-a")
	withInbox(node)
	if _, err := node.reg.AddAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	node.daemon.discoverCatalog(context.Background())
	scout := identityID(t, node, "scout")
	var advertised []string
	node.daemon.registerRecentClaudePeers(context.Background(), func(_ context.Context, sessions []surface.Session) map[string]error {
		for _, session := range sessions {
			advertised = append(advertised, session.ID)
		}
		return nil
	})
	if !slices.Contains(advertised, scout) {
		t.Fatalf("advertised %v, want scout", advertised)
	}
	active, err := node.reg.CatalogActiveSessions(time.Now().Add(2 * surface.ActiveWindow))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(active, func(state registry.CatalogSessionState) bool { return state.Session.ID == scout }) {
		t.Fatal("an identity quiet for longer than the active window dropped out of the peer candidates")
	}
}

func TestASendToAnIdentityOnAnUnreachableHostLandsOnceWhenItReturns(t *testing.T) {
	laptop, box, boxRecord, _ := pairedWithIdentity(t)
	scout := identityID(t, box, "scout")
	box.gate.proxyDown.Store(true)
	send := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(boxRecord.ID, scout), "sourceSessionId": "laptop-a", "message": "while you were away"})
	if send.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	if messages := inbox(t, box, "scout"); len(messages) != 0 {
		t.Fatalf("delivered while the box was unreachable: %+v", messages)
	}
	host, _ := laptop.daemon.pairedHost(boxRecord.ID)
	box.gate.proxyDown.Store(false)
	testkit.Eventually(t, "held send reaches scout's inbox", 10*time.Second, func() bool {
		laptop.daemon.drainHostDeliveries(context.Background(), host)
		return len(inbox(t, box, "scout")) > 0
	})
	for range 3 {
		laptop.daemon.drainHostDeliveries(context.Background(), host)
	}
	messages := inbox(t, box, "scout")
	if len(messages) != 1 || !strings.Contains(messages[0].Message, "while you were away") {
		t.Fatalf("scout's inbox=%+v", messages)
	}
	queue, err := laptop.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "delivered" {
		t.Fatalf("laptop queue=%+v err=%v", queue, err)
	}
}

func TestRemovingAnIdentityRetiresItsMirror(t *testing.T) {
	laptop, box, boxRecord, _ := pairedWithIdentity(t)
	scout := identityID(t, box, "scout")
	if err := box.reg.RemoveAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	for range catalogOmissionThreshold {
		box.daemon.discoverCatalog(context.Background())
	}
	send := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(boxRecord.ID, scout), "sourceSessionId": "laptop-a", "message": "anyone there"})
	if send.Code == http.StatusOK || !strings.Contains(send.Body.String(), "no such agent identity") {
		t.Fatalf("a send to a removed identity status=%d body=%s", send.Code, send.Body.String())
	}
	testkit.Eventually(t, "the laptop drops its mirror of the removed identity", 10*time.Second, func() bool {
		rows, err := laptop.reg.HostCatalogRows(boxRecord.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Session.ID == scout {
				return false
			}
		}
		return true
	})
	if _, err := box.reg.AgentIdentityNamed("scout"); err == nil {
		t.Fatal("scout is still an identity")
	}
}
