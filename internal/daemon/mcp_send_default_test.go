package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func busyTarget(t *testing.T, r *registry.Registry, fake *daemonSurface, to surface.Session) {
	t.Helper()
	fake.caps = surface.Capabilities{Send: true, Steer: true}
	fake.rejectBusy = true
	to.Status = surface.StatusBusy
	fake.sessions[to.ID] = to
	fake.observations[to.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	if err := r.RecordObservedSession(to); err != nil {
		t.Fatal(err)
	}
}

func steeredText(fake *daemonSurface, text string) bool {
	return slices.ContainsFunc(fake.steered, func(m string) bool { return strings.Contains(m, text) })
}

func TestASendWithNoModeSteersABusySessionTheSenderStartedUnlessItSaysQueue(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	busyTarget(t, r, fake, to)
	if err := r.RecordSessionOrigin(registry.OriginOf(to.ID, from.ID)); err != nil {
		t.Fatal(err)
	}
	lead := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from))

	if out, refused := callMCP(t, lead, "agenthail_send", map[string]any{"target": to.ID, "message": "the answer is left"}); refused || out["notice"] != nil {
		t.Fatalf("a lead's send with no mode to its busy worker: %v", out)
	}
	if out, refused := callMCP(t, lead, "agenthail_send", map[string]any{"target": to.ID, "message": "later, please", "mode": "queue"}); refused {
		t.Fatalf("a lead's queued send: %v", out)
	}
	if !steeredText(fake, "the answer is left") || steeredText(fake, "later, please") || r.QueueCount(to.ID) != 1 {
		t.Fatalf("only the send with no mode should steer: steered=%q queued=%d", fake.steered, r.QueueCount(to.ID))
	}
}

func TestASendWithNoModeToABusySessionTheSenderDoesNotLeadQueues(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	busyTarget(t, r, fake, to)
	peer := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from))

	out, refused := callMCP(t, peer, "agenthail_send", map[string]any{"target": to.ID, "message": "when you have a moment"})
	if refused || out["notice"] != nil {
		t.Fatalf("an unrelated send with no mode: %v", out)
	}
	if len(fake.steered) != 0 || r.QueueCount(to.ID) != 1 {
		t.Fatalf("an unrelated send steered: steered=%q queued=%d", fake.steered, r.QueueCount(to.ID))
	}
}

func pairedBusyWorker(t *testing.T) (hostTargets, *toldPerson) {
	t.Helper()
	h := newHostTargets(t, true)
	person := &toldPerson{}
	h.local.daemon.notifyDesktop = person.notify
	lead := surface.UnrepliableSender(mustHostID(t, h.local), "local-a", "").ID
	if err := h.remote.reg.EnsureSender(lead); err != nil {
		t.Fatal(err)
	}
	if err := h.remote.reg.RecordSessionOrigin(registry.OriginOf("remote-c", lead)); err != nil {
		t.Fatal(err)
	}
	h.remote.fake.mu.Lock()
	h.remote.fake.rejectBusy = true
	worker := h.remote.fake.sessions["remote-c"]
	worker.Status = surface.StatusBusy
	h.remote.fake.sessions["remote-c"] = worker
	h.remote.fake.mu.Unlock()
	return h, person
}

func TestALeadsSendWithNoModeSteersItsBusyWorkerOnAPairedHostWithoutTellingThePerson(t *testing.T) {
	h, person := pairedBusyWorker(t)

	if out, refused := h.send(t, "vps::@builder", "the answer is left", nil); refused || out["notice"] != nil || !h.reached("steer:") {
		t.Fatalf("a lead's send with no mode to its busy worker on the paired host: refused=%v out=%v delivered=%q", refused, out, h.remote.fake.delivered())
	}
	if out, refused := h.send(t, "vps::@builder", "later, please", map[string]any{"mode": "queue"}); refused || h.reached("steer:"+"later") {
		t.Fatalf("a lead's queued send steered: refused=%v out=%v delivered=%q", refused, out, h.remote.fake.delivered())
	}
	if told := person.told(); len(told) != 0 {
		t.Fatalf("a steer Agenthail chose told the person %q", told)
	}
}

func TestALeadsDefaultSteerItsRoleRefusesQueuesAndSaysSoOnlyToTheLead(t *testing.T) {
	h, person := pairedBusyWorker(t)
	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Ask)

	out, refused := h.send(t, "vps::@builder", "the answer is left", nil)
	notice, _ := out["notice"].(string)
	if refused || !strings.HasPrefix(notice, "Queued: a send to a session you started or lead goes now by default, but this one could not") {
		t.Fatalf("a default steer the lead's role refuses: refused=%v out=%v", refused, out)
	}
	if h.reached("steer:") {
		t.Fatalf("a refused default steer steered: %q", h.remote.fake.delivered())
	}
	if told := person.told(); len(told) != 0 {
		t.Fatalf("a refused default steer told the person %q", told)
	}
}

func TestARefusedDefaultSteerWithTheLongestClientRequestIDStillQueues(t *testing.T) {
	h, _ := pairedBusyWorker(t)
	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Ask)
	longest := strings.Repeat("k", registry.ActionReceiptKeyLimit-len("agenthail_send:"))

	out, refused := callMCP(t, h.caller, "agenthail_send", map[string]any{"target": "vps::@builder", "message": "the answer is left", "clientRequestId": longest})
	notice, _ := out["notice"].(string)
	if refused || !strings.HasPrefix(notice, "Queued: a send to a session you started or lead goes now by default") || h.remote.reg.QueueCount("remote-c") != 1 {
		t.Fatalf("a refused default steer with the longest clientRequestId: refused=%v out=%v", refused, out)
	}
}

func TestARetriedDefaultSteerTheOwningHostRefusedReplaysItsQueuedResult(t *testing.T) {
	h, _ := pairedBusyWorker(t)
	if _, err := h.local.reg.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES('local-a','{"permission":"edits"}')`); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"target": "vps::@builder", "message": "the answer is left", "clientRequestId": "answer-1"}

	first, refused := callMCP(t, h.caller, "agenthail_send", args)
	notice, _ := first["notice"].(string)
	if refused || !strings.Contains(notice, "but this one could not") || h.reached("steer:") {
		t.Fatalf("a default steer the owning host refuses: refused=%v out=%v delivered=%q", refused, first, h.remote.fake.delivered())
	}
	audited := len(mcpAudits(t, h.local.reg))
	again, refused := callMCP(t, h.caller, "agenthail_send", args)
	if refused || mustJSONString(t, again) != mustJSONString(t, first) {
		t.Fatalf("the retry answered %v, want the first answer %v", again, first)
	}
	if queued := h.remote.reg.QueueCount("remote-c"); queued != 1 || h.reached("steer:") {
		t.Fatalf("the retry acted again: queued=%d delivered=%q", queued, h.remote.fake.delivered())
	}
	if audits := mcpAudits(t, h.local.reg); len(audits) != audited || !strings.Contains(audits[len(audits)-1].Message, "answer-1") {
		t.Fatalf("audits after the retry=%+v, want the first call's %d naming answer-1", audits, audited)
	}
}

func TestALeadsDefaultSteerTellsThePersonWhenTheirRoleSaysNotify(t *testing.T) {
	h, person := pairedBusyWorker(t)
	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Notify)

	if out, refused := h.send(t, "vps::@builder", "the answer is left", nil); refused || !h.reached("steer:") {
		t.Fatalf("a lead's default steer under notify: refused=%v out=%v delivered=%q", refused, out, h.remote.fake.delivered())
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "@builder on vps") {
		t.Fatalf("the person was told %q, want one notice of the steer", told)
	}
}
