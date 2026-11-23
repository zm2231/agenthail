package daemon

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestALeadSteersItsProjectsSessionOnAPairedHostQuietlyUnlessThePersonAsksToBeTold(t *testing.T) {
	h := newHostTargets(t, true)
	person := &toldPerson{}
	h.local.daemon.notifyDesktop = person.notify
	steer := func(message string) (map[string]any, bool) {
		return callMCP(t, h.caller, "agenthail_steer", map[string]any{"target": "vps::@builder", "message": message, "clientRequestId": "steer-" + message})
	}

	if out, refused := steer("turn left"); refused || !h.reached("turn left") {
		t.Fatalf("a lead steering its project's session: refused=%v out=%v", refused, out)
	}
	if out, refused := h.send(t, "vps::remote-c", "sent now", map[string]any{"mode": "now"}); refused || !h.reached("sent now") {
		t.Fatalf("a lead sending now: refused=%v out=%v", refused, out)
	}
	if told := person.told(); len(told) != 0 {
		t.Fatalf("the person was told about a lead steering its own session: %q", told)
	}

	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Notify)
	if out, refused := steer("told now"); refused || !h.reached("told now") {
		t.Fatalf("a steer the person hears about: refused=%v out=%v", refused, out)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "@builder on vps") {
		t.Fatalf("with overseen-control notify the person was told %q", told)
	}

	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Allow)
	setRoleDecision(t, h.local.reg, agentroles.Lead, "remote-project-control", agentroles.Ask)
	if out, refused := steer("still the lead's"); refused || !h.reached("still the lead's") {
		t.Fatalf("remote-project-control ask stopped a lead steering a session it leads: refused=%v out=%v", refused, out)
	}

	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Ask)
	out, refused := steer("the person's to allow")
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	if h.reached("the person's to allow") {
		t.Fatal("a steer the role leaves to the person reached the paired host")
	}
	if told := person.told(); len(told) != 1 {
		t.Fatalf("the person was told %q", told)
	}
}

func TestTheOwningHostDecidesWhetherTheCallerStartedTheSessionItControls(t *testing.T) {
	h := newHostTargets(t, false)
	person := &toldPerson{}
	h.local.daemon.notifyDesktop = person.notify
	changeRole(t, h.local.reg, agentroles.Worker, func(role *agentroles.Role) { role.Reach = agentroles.ReachProject })
	creator, err := h.remote.reg.EnsureUnrepliableSender(mustHostID(t, h.local), "local-a", "local-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.remote.reg.RecordSessionOrigin(registry.OriginOf("remote-c", creator)); err != nil {
		t.Fatal(err)
	}

	if out, refused := h.send(t, "vps::@builder", "to the one it started", map[string]any{"mode": "now"}); refused || !h.reached("to the one it started") {
		t.Fatalf("sending now to a session it started: refused=%v out=%v", refused, out)
	}
	if told := person.told(); len(told) != 0 {
		t.Fatalf("the person was told about a session the caller started: %q", told)
	}
	if out, refused := h.send(t, "vps::remote-a", "to one it neither started nor leads", map[string]any{"mode": "now"}); refused || !h.reached("to one it neither started nor leads") {
		t.Fatalf("sending now to another session in its project: refused=%v out=%v", refused, out)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "on vps") {
		t.Fatalf("the person was told %q", told)
	}

	setRoleDecision(t, h.local.reg, agentroles.Worker, "overseen-control", agentroles.Ask)
	if out, refused := h.send(t, "vps::@builder", "kept for the person", map[string]any{"mode": "now"}); !refused || h.reached("kept for the person") {
		t.Fatalf("the owning host let through what the role leaves to the person: refused=%v out=%v", refused, out)
	}
}

func TestTheCLIToldOfAnActionItsHostAllowedQuietlyTellsThePersonNothing(t *testing.T) {
	h := newHostTargets(t, true)
	person := &toldPerson{}
	h.local.daemon.notifyDesktop = person.notify
	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Notify)
	target := hosts.Qualify(h.record.ID, "remote-c")
	for _, decision := range []string{agentroles.Allow, agentroles.Notify, ""} {
		answer := askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "control", Payload: scopePayload(daemonapi.PersonActionAPI, `{"action":"steer","sessionId":"`+target+`","message":"turn left"}`)}, "127.0.0.1:50000")
		if answer.status != http.StatusOK || answer.reply.Notice == "" || answer.reply.ScopeOverseen != agentroles.Notify {
			t.Fatalf("a lead's scope check for a steer: %+v body=%s", answer, answer.body)
		}
		askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "notice", Notice: answer.reply.Notice, ControlDecision: decision}, "127.0.0.1:50000")
	}
	if told := person.told(); len(told) != 2 {
		t.Fatalf("the person was told %q, want the notify and the unreported decisions only", told)
	}
}

func TestAnAgentSteersASessionItStartedOnThisHostAsItsRoleDecides(t *testing.T) {
	d, r, _, worker, child := daemonFixture(t)
	person := &toldPerson{}
	d.notifyDesktop = person.notify
	if err := r.RecordSessionOrigin(registry.OriginOf(child.ID, worker.ID)); err != nil {
		t.Fatal(err)
	}
	const local = "127.0.0.1:50000"
	now := daemonapi.ScopeRequest{SessionID: worker.ID, Action: "send", Target: child.ID, Mode: "now"}

	if quiet := askScope(t, d, now, local); quiet.status != http.StatusOK || quiet.reply.TargetID != child.ID || quiet.reply.Notice != "" {
		t.Fatalf("sending now to a session it started: %+v", quiet)
	}

	setRoleDecision(t, r, agentroles.Worker, "overseen-control", agentroles.Notify)
	held := askScope(t, d, now, local)
	if held.status != http.StatusOK || held.reply.Notice == "" {
		t.Fatalf("with overseen-control notify: %+v", held)
	}
	askScope(t, d, daemonapi.ScopeRequest{SessionID: worker.ID, Action: "notice", Notice: held.reply.Notice}, local)
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "sent now to") {
		t.Fatalf("the person was told %q", told)
	}

	setRoleDecision(t, r, agentroles.Worker, "overseen-control", agentroles.Ask)
	if refused := askScope(t, d, now, local); refused.code != "out_of_scope" {
		t.Fatalf("with overseen-control ask: %+v", refused)
	}
	if queued := askScope(t, d, daemonapi.ScopeRequest{SessionID: worker.ID, Action: "send", Target: child.ID}, local); queued.status != http.StatusOK {
		t.Fatalf("a queued message is not overseen control: %+v", queued)
	}
}
