package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// phoneRelay stands in for the push relay: each installation names the phone
// it reaches, and a send whose dedupe key that phone already got is answered
// as delivered without reaching it again.
type phoneRelay struct {
	mu        sync.Mutex
	phones    map[string]string
	sends     []map[string]string
	delivered map[string][]map[string]string
	seen      map[string]bool
}

func newPhoneRelay(t *testing.T) *phoneRelay {
	t.Helper()
	relay := &phoneRelay{phones: map[string]string{}, delivered: map[string][]map[string]string{}, seen: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		relay.mu.Lock()
		relay.sends = append(relay.sends, payload)
		phone := relay.phones[payload["installationId"]]
		answer := `{"ok":true}`
		if key := payload["dedupeKey"]; key != "" && relay.seen[phone+key] {
			answer = `{"ok":true,"deduped":true}`
		} else {
			relay.seen[phone+key] = key != ""
			relay.delivered[phone] = append(relay.delivered[phone], payload)
		}
		relay.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	return relay
}

// pairPhone pairs the phone with a computer, which registers it with the relay
// under an installation of its own.
func (r *phoneRelay) pairPhone(t *testing.T, reg *registry.Registry, phone, installation string) {
	t.Helper()
	pairing, err := reg.CreateDevicePairing("Phone", []string{"read", "control"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := reg.CompleteDevicePairing(pairing.Secret, "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SaveDevicePushTarget(device.ID, installation, "credential"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.phones[installation] = phone
	r.mu.Unlock()
}

func (r *phoneRelay) shown(phone string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]string(nil), r.delivered[phone]...)
}

func (r *phoneRelay) sent() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]string(nil), r.sends...)
}

type mirroredNotices struct {
	mu      sync.Mutex
	notices []RequestPush
}

func recordDesktop(d *Daemon) *mirroredNotices {
	recorded := &mirroredNotices{}
	d.notifyDesktop = func(_, message, _, _, requestID string, actions []surface.RequestAction) error {
		recorded.mu.Lock()
		defer recorded.mu.Unlock()
		recorded.notices = append(recorded.notices, RequestPush{message, requestID, actions})
		return nil
	}
	return recorded
}

func (n *mirroredNotices) list() []RequestPush {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]RequestPush(nil), n.notices...)
}

// withoutDesktop is a Linux host: it has no desktop notifications to raise.
func withoutDesktop(d *Daemon) {
	d.notifyDesktop = func(string, string, string, string, string, []surface.RequestAction) error {
		return errors.New("desktop notifications are not supported on linux")
	}
}

type scriptedTurn struct {
	*federatedSurface
	observation *surface.TurnObservation
}

func (s scriptedTurn) Observe(context.Context, *surface.Session) (*surface.TurnObservation, error) {
	return s.observation, nil
}

// hostTurn runs one turn of a session on the host as its observer sees it,
// then lets the host republish its catalog, as its discovery loop would.
func hostTurn(t *testing.T, host *federationNode, sessionID, turnID, failure string, during func()) {
	t.Helper()
	host.fake.mu.Lock()
	session := host.fake.sessions[sessionID]
	host.fake.mu.Unlock()
	observe := func(observation *surface.TurnObservation) {
		host.daemon.observeSession(t.Context(), scriptedTurn{host.fake, observation}, &session)
	}
	if _, found, err := host.reg.RuntimeState(sessionID); err != nil {
		t.Fatal(err)
	} else if !found {
		observe(&surface.TurnObservation{Status: surface.StatusIdle})
	}
	observe(&surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: turnID})
	if during != nil {
		during()
	}
	observe(&surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turnID, Reply: &surface.ReplyResult{Done: true, Error: failure}})
	host.daemon.discoverCatalog(context.Background())
}

func awaitHostNotice(t *testing.T, host *federationNode, sessionID, turnID string) {
	t.Helper()
	testkit.Eventually(t, "the host to record its notice", 5*time.Second, func() bool {
		notices, err := host.reg.TurnNotices()
		return err == nil && notices[sessionID].TurnID == turnID
	})
}

func awaitMirroredCompletion(t *testing.T, viewer *federationNode, sessionID, turnID string) {
	t.Helper()
	testkit.Eventually(t, "the completion mirrored to the viewer", 10*time.Second, func() bool {
		stored, _, err := viewer.reg.CatalogProjection(sessionID)
		completion := mirroredTurnCompletion(stored)
		return err == nil && completion != nil && completion.TurnID == turnID
	})
}

var settleMarks atomic.Int32

// settleNotices returns once every notice the host's earlier catalog events could raise has been sent. The host's stream is ordered, so a marker row mirrored on each viewer means every earlier event was handled and its notice started; draining background work then finishes the sends.
func settleNotices(t *testing.T, host *federationNode, hostID string, viewers ...*federationNode) {
	t.Helper()
	name := fmt.Sprintf("settled %d", settleMarks.Add(1))
	host.fake.mu.Lock()
	session := host.fake.sessions["remote-a"]
	session.Name, session.LastActive = name, time.Now()
	host.fake.sessions["remote-a"] = session
	host.fake.mu.Unlock()
	host.daemon.discoverCatalog(context.Background())
	for _, viewer := range viewers {
		testkit.Eventually(t, "the viewer to mirror the marker", 10*time.Second, func() bool {
			for _, row := range sessionsOf(viewer.snapshot(t), hostID) {
				if row.Name == name {
					return true
				}
			}
			return false
		})
		viewer.daemon.background.wait()
	}
	host.daemon.background.wait()
}

func TestATurnOnAPairedLinuxHostNotifiesTheViewerAndThePhoneOnce(t *testing.T) {
	relay := newPhoneRelay(t)
	viewer, host, record := pairedFederation(t)
	withoutDesktop(host.daemon)
	mac := recordDesktop(viewer.daemon)
	relay.pairPhone(t, host.reg, "phone", "via-host")
	relay.pairPhone(t, viewer.reg, "phone", "via-viewer")
	qualified := hosts.Qualify(record.ID, "remote-a")

	hostTurn(t, host, "remote-a", "turn-1", "", nil)
	testkit.Eventually(t, "both computers to send the push", 10*time.Second, func() bool { return len(relay.sent()) == 2 })
	settleNotices(t, host, record.ID, viewer)
	sends := relay.sent()
	if sends[0]["dedupeKey"] == "" || sends[0]["dedupeKey"] != sends[1]["dedupeKey"] {
		t.Fatalf("the two computers named the notification differently: %+v", sends)
	}
	for _, send := range sends {
		if !strings.HasPrefix(send["message"], "Codex agent finished on ") {
			t.Fatalf("a push without the app, status and host: %+v", send)
		}
	}
	if shown := relay.shown("phone"); len(shown) != 1 {
		t.Fatalf("the phone showed %d notifications: %+v", len(shown), shown)
	}
	notices := mac.list()
	if len(notices) != 1 || notices[0].message != "Codex agent finished on vps" {
		t.Fatalf("viewer desktop notices %+v", notices)
	}
	for _, send := range sends {
		if send["installationId"] == "via-viewer" && send["sessionId"] != qualified {
			t.Fatalf("the viewer's push names session %q, not %q", send["sessionId"], qualified)
		}
	}
	settleNotices(t, host, record.ID, viewer)
	if notices := mac.list(); len(notices) != 1 {
		t.Fatalf("a later change to the row notified its turn again: %+v", notices)
	}

	hostTurn(t, host, "remote-a", "turn-2", "the model refused", nil)
	testkit.Eventually(t, "the failure notified", 10*time.Second, func() bool { return len(mac.list()) == 2 })
	settleNotices(t, host, record.ID, viewer)
	if notices := mac.list(); notices[1].message != "Codex agent failed on vps" {
		t.Fatalf("failure notice %+v", notices[1])
	}
	if shown := relay.shown("phone"); len(shown) != 2 || !strings.Contains(shown[1]["message"], "failed") {
		t.Fatalf("after the failure the phone showed %+v", shown)
	}
}

func TestEachViewingMacNotifiesItsOwnScreenWhileThePhoneGetsOne(t *testing.T) {
	relay := newPhoneRelay(t)
	first, host, record := pairedFederation(t)
	withoutDesktop(host.daemon)
	second := newFederationNode(t, "local-b")
	second.start(t, testRoots(host.server))
	second.pair(t, host, "vps")
	testkit.Eventually(t, "the second viewer mirrors the host", 10*time.Second, func() bool {
		return len(sessionsOf(second.snapshot(t), record.ID)) == 2
	})
	macs := []*mirroredNotices{recordDesktop(first.daemon), recordDesktop(second.daemon)}
	relay.pairPhone(t, first.reg, "phone", "via-first")
	relay.pairPhone(t, second.reg, "phone", "via-second")

	hostTurn(t, host, "remote-b", "turn-1", "", nil)
	testkit.Eventually(t, "both viewers to send the push", 10*time.Second, func() bool { return len(relay.sent()) == 2 })
	settleNotices(t, host, record.ID, first, second)
	for index, mac := range macs {
		if notices := mac.list(); len(notices) != 1 {
			t.Fatalf("viewer %d desktop notices %+v", index, notices)
		}
	}
	if shown := relay.shown("phone"); len(shown) != 1 {
		t.Fatalf("the phone showed %+v", shown)
	}
}

func TestAPairedHostsTurnsWhileItWasUnreachableNeverNotify(t *testing.T) {
	relay := newPhoneRelay(t)
	viewer, host, record := pairedFederationWith(t, func(f *federation) {
		f.downGrace, f.retryMin = time.Hour, 50*time.Millisecond
	})
	withoutDesktop(host.daemon)
	mac := recordDesktop(viewer.daemon)
	relay.pairPhone(t, viewer.reg, "phone", "via-viewer")

	hostTurn(t, host, "remote-a", "turn-1", "", nil)
	testkit.Eventually(t, "the live turn notified", 10*time.Second, func() bool { return len(mac.list()) == 1 })

	refused := host.gate.proxyRefusals.Load()
	host.gate.proxyDown.Store(true)
	host.server.CloseClientConnections()
	testkit.Eventually(t, "the viewer to find the host unreachable", 10*time.Second, func() bool {
		return host.gate.proxyRefusals.Load()-refused >= 2
	})
	hostTurn(t, host, "remote-a", "turn-2", "", nil)
	awaitHostNotice(t, host, "remote-a", "turn-2")
	streams := host.gate.streams.Load()
	host.gate.proxyDown.Store(false)
	awaitNewStream(t, host, streams)
	awaitMirroredCompletion(t, viewer, "remote-a", "turn-2")
	settleNotices(t, host, record.ID, viewer)
	if notices := mac.list(); len(notices) != 1 {
		t.Fatalf("a turn finished while the host was unreachable notified on reconnect: %+v", notices)
	}
	if shown := relay.shown("phone"); len(shown) != 1 {
		t.Fatalf("the phone showed %+v", shown)
	}

	hostTurn(t, host, "remote-a", "turn-3", "", nil)
	testkit.Eventually(t, "a live turn after the outage notified", 10*time.Second, func() bool { return len(mac.list()) == 2 })
}

func TestARestartedViewerDoesNotNotifyTurnsItMissed(t *testing.T) {
	relay := newPhoneRelay(t)
	viewer, host, record := pairedFederation(t)
	withoutDesktop(host.daemon)
	mac := recordDesktop(viewer.daemon)
	relay.pairPhone(t, viewer.reg, "phone", "via-viewer")

	hostTurn(t, host, "remote-a", "turn-1", "", nil)
	testkit.Eventually(t, "the live turn notified", 10*time.Second, func() bool { return len(mac.list()) == 1 })
	viewer.cancel()
	viewer.daemon.stopFederation()
	hostTurn(t, host, "remote-a", "turn-2", "", nil)
	awaitHostNotice(t, host, "remote-a", "turn-2")
	viewer.start(t, testRoots(host.server))
	awaitMirroredCompletion(t, viewer, "remote-a", "turn-2")
	settleNotices(t, host, record.ID, viewer)
	if notices := mac.list(); len(notices) != 1 {
		t.Fatalf("a restarted viewer notified a turn it missed: %+v", notices)
	}
	if shown := relay.shown("phone"); len(shown) != 1 {
		t.Fatalf("the phone showed %+v", shown)
	}
}

func TestATurnFirstSeenAfterAStreamGapDoesNotNotify(t *testing.T) {
	relay := newPhoneRelay(t)
	viewer, host, record := pairedFederationWith(t, func(f *federation) { f.retryMin = 50 * time.Millisecond })
	withoutDesktop(host.daemon)
	mac := recordDesktop(viewer.daemon)
	relay.pairPhone(t, viewer.reg, "phone", "via-viewer")

	hostTurn(t, host, "remote-a", "turn-1", "", nil)
	testkit.Eventually(t, "the live turn notified", 10*time.Second, func() bool { return len(mac.list()) == 1 })
	missed := func() { hostTurn(t, host, "remote-a", "turn-2", "", nil) }
	host.gate.beforeGap.Store(&missed)
	snapshots := host.gate.snapshotsServed.Load()
	host.server.CloseClientConnections()
	testkit.Eventually(t, "the stream to answer with a gap", 10*time.Second, func() bool { return host.gate.gaps.Load() == 1 })
	awaitMirroredCompletion(t, viewer, "remote-a", "turn-2")
	if host.gate.snapshotsServed.Load()-snapshots < 2 {
		t.Fatal("the viewer did not take a snapshot after the gap")
	}
	settleNotices(t, host, record.ID, viewer)
	if notices := mac.list(); len(notices) != 1 {
		t.Fatalf("a turn read from the snapshot after a gap notified: %+v", notices)
	}
	if shown := relay.shown("phone"); len(shown) != 1 {
		t.Fatalf("the phone showed %+v", shown)
	}
}

func TestAnAskOnAPairedHostNotifiesWithItsButtonsAndIsAnsweredThroughTheViewer(t *testing.T) {
	relay := newPhoneRelay(t)
	viewer, host, record := pairedFederation(t)
	withoutDesktop(host.daemon)
	mac := recordDesktop(viewer.daemon)
	relay.pairPhone(t, host.reg, "phone", "via-host")
	relay.pairPhone(t, viewer.reg, "phone", "via-viewer")
	qualified := hosts.Qualify(record.ID, "remote-a")
	ask := registry.TurnEnd{SessionID: "remote-a", Needs: "you", Headline: "Ready to ship", Source: registry.TurnSourceTool,
		Asks: []registry.TurnAsk{{Question: "Deploy it now?", Options: []registry.TurnOption{{Label: "Deploy"}, {Label: "Wait"}}}}}

	hostTurn(t, host, "remote-a", "turn-1", "", func() {
		if _, err := host.reg.EndTurn(ask, "turn-1", time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	testkit.Eventually(t, "both computers to send the ask", 10*time.Second, func() bool { return len(relay.sent()) == 2 })
	settleNotices(t, host, record.ID, viewer)
	sends := relay.sent()
	if sends[0]["requestId"] == "" || sends[0]["requestId"] != sends[1]["requestId"] || sends[0]["dedupeKey"] != sends[1]["dedupeKey"] {
		t.Fatalf("the two pushes are not the same ask: %+v", sends)
	}
	notices := mac.list()
	if len(notices) != 1 || notices[0].requestID != sends[0]["requestId"] || len(notices[0].actions) != 3 || notices[0].actions[1].ID != "o1" {
		t.Fatalf("viewer desktop notice %+v", notices)
	}
	if shown := relay.shown("phone"); len(shown) != 1 {
		t.Fatalf("the phone showed %+v", shown)
	}

	response := viewer.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": qualified, "requestId": sends[0]["requestId"], "decision": map[string]any{"choice": "o1"}})
	if response.Code != http.StatusOK {
		t.Fatalf("answer through the viewer: %d %s", response.Code, response.Body.String())
	}
	if delivered := host.fake.delivered(); len(delivered) != 1 || delivered[0] != "1. Deploy it now? Wait" {
		t.Fatalf("the host's session got %q", delivered)
	}
	end, found, err := host.reg.CurrentTurnEnd("remote-a")
	if err != nil || !found || end.AnsweredAt.IsZero() {
		t.Fatalf("the host's ask is not answered: %+v found=%v err=%v", end, found, err)
	}
}

func TestASessionFirstSeenWithAFinishedTurnDoesNotNotify(t *testing.T) {
	relay := newPhoneRelay(t)
	viewer, host, record := pairedFederation(t)
	withoutDesktop(host.daemon)
	mac := recordDesktop(viewer.daemon)
	relay.pairPhone(t, viewer.reg, "phone", "via-viewer")
	host.fake.mu.Lock()
	session := host.fake.sessions["remote-a"]
	session.ID, session.Name = "remote-new", "work remote-new"
	host.fake.sessions[session.ID] = session
	host.fake.mu.Unlock()
	if err := host.reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}

	hostTurn(t, host, "remote-new", "turn-1", "", nil)
	awaitMirroredCompletion(t, viewer, "remote-new", "turn-1")
	settleNotices(t, host, record.ID, viewer)
	if notices := mac.list(); len(notices) != 0 {
		t.Fatalf("a row first seen with its turn already finished notified: %+v", notices)
	}
	hostTurn(t, host, "remote-new", "turn-2", "", nil)
	testkit.Eventually(t, "its next turn notified", 10*time.Second, func() bool { return len(mac.list()) == 1 })
	settleNotices(t, host, record.ID, viewer)
	if shown := relay.shown("phone"); len(shown) != 1 || shown[0]["sessionId"] != hosts.Qualify(record.ID, "remote-new") {
		t.Fatalf("the phone showed %+v", shown)
	}
}
