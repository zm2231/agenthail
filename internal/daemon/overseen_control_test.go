package daemon

import (
	"encoding/json"
	"fmt"
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

func TestAnInterruptOnAPairedHostFollowsTheDecisionForASessionTheCallerStarted(t *testing.T) {
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
	interrupt := func(key string) (map[string]any, bool) {
		return callMCP(t, h.caller, "agenthail_interrupt", map[string]any{"target": "vps::@builder", "clientRequestId": key})
	}

	if out, refused := interrupt("allowed"); refused || h.remote.fake.interrupts() != 1 {
		t.Fatalf("interrupting a session it started: refused=%v out=%v interrupts=%d", refused, out, h.remote.fake.interrupts())
	}
	if told := person.told(); len(told) != 0 {
		t.Fatalf("the person was told about an interrupt overseen-control allows: %q", told)
	}

	setRoleDecision(t, h.local.reg, agentroles.Worker, "overseen-control", agentroles.Notify)
	if out, refused := interrupt("told"); refused || h.remote.fake.interrupts() != 2 {
		t.Fatalf("an interrupt the person hears about: refused=%v out=%v", refused, out)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Interrupted @builder on vps") {
		t.Fatalf("with overseen-control notify the person was told %q", told)
	}

	setRoleDecision(t, h.local.reg, agentroles.Worker, "overseen-control", agentroles.Ask)
	if out, refused := interrupt("kept"); !refused || h.remote.fake.interrupts() != 2 {
		t.Fatalf("an interrupt the role leaves to the person: refused=%v out=%v interrupts=%d", refused, out, h.remote.fake.interrupts())
	}
	if told := person.told(); len(told) != 1 {
		t.Fatalf("the person was told %q", told)
	}
}

func TestALeadChangingItsOwnWorkerOnAPairedHostFollowsOverseenControl(t *testing.T) {
	h := newHostTargets(t, true)
	person := &toldPerson{}
	h.local.daemon.notifyDesktop = person.notify
	change := func(tool, key string, args map[string]any) (map[string]any, bool) {
		args["clientRequestId"] = key
		return callMCP(t, h.caller, tool, args)
	}
	pin := func(sessionID string) map[string]any { return h.pinAsTheCLIDoes(t, "local-a", sessionID) }

	if out, refused := change("agenthail_archive", "archive-quiet", map[string]any{"target": "vps::@builder"}); refused || out["controlDecision"] != agentroles.Allow {
		t.Fatalf("a lead archiving its own worker: refused=%v out=%v", refused, out)
	}
	if out, refused := change("agenthail_alias", "alias-quiet", map[string]any{"target": "vps::remote-a", "name": "helper"}); refused || out["controlDecision"] != agentroles.Allow {
		t.Fatalf("a lead renaming its own worker: refused=%v out=%v", refused, out)
	}
	if named, err := h.remote.reg.LookupAlias("helper"); err != nil || named != "remote-a" {
		t.Fatalf("the paired host's alias: %q %v", named, err)
	}
	if answer := pin("remote-a"); answer["controlDecision"] != agentroles.Allow {
		t.Fatalf("a lead pinning its own worker: %v", answer)
	}
	if told := person.told(); len(told) != 0 {
		t.Fatalf("the person was told %q of a lead changing its own workers", told)
	}

	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Notify)
	setRoleDecision(t, h.local.reg, agentroles.Lead, "remote-project-control", agentroles.Allow)
	if out, refused := change("agenthail_alias", "alias-told", map[string]any{"target": "vps::@helper", "name": "reviewer"}); refused || out["controlDecision"] != agentroles.Notify {
		t.Fatalf("a rename the person hears about: refused=%v out=%v", refused, out)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Renamed ") || !strings.Contains(told[0], " on vps") {
		t.Fatalf("with overseen-control notify the person was told %q", told)
	}
	if answer := pin("remote-a"); answer["controlDecision"] != agentroles.Notify {
		t.Fatalf("a pin under overseen-control notify: %v", answer)
	}

	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Ask)
	out, refused := change("agenthail_archive", "archive-kept", map[string]any{"target": "vps::remote-a"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	if archived, err := h.remote.reg.SessionArchived("remote-a"); err != nil || archived {
		t.Fatalf("archived=%v err=%v, want an archive the role leaves to the person kept off the paired host", archived, err)
	}
}

func TestRemoteProjectControlStillDecidesChangesToSessionsTheCallerDoesNotOversee(t *testing.T) {
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
	rename := func(target, name string) (map[string]any, bool) {
		return callMCP(t, h.caller, "agenthail_alias", map[string]any{"target": target, "name": name, "clientRequestId": "alias-" + name})
	}

	if out, refused := rename("vps::@builder", "started"); refused || out["controlDecision"] != agentroles.Allow {
		t.Fatalf("renaming a session it started: refused=%v out=%v", refused, out)
	}
	if answer := h.pinAsTheCLIDoes(t, "local-a", "remote-a"); answer["controlDecision"] != agentroles.Notify {
		t.Fatalf("pinning a session in its project it neither started nor leads: %v", answer)
	}

	setRoleDecision(t, h.local.reg, agentroles.Worker, "remote-project-control", agentroles.Ask)
	if out, refused := rename("vps::@started", "still-mine"); refused || out["controlDecision"] != agentroles.Allow {
		t.Fatalf("remote-project-control ask stopped renaming a session the caller started: refused=%v out=%v", refused, out)
	}
	if out, refused := callMCP(t, h.caller, "agenthail_archive", map[string]any{"target": "vps::remote-a", "clientRequestId": "archive-not-mine"}); !refused || !strings.Contains(fmt.Sprint(out["message"]), "leaves this action") {
		t.Fatalf("archiving a session the caller does not oversee under remote-project-control ask: refused=%v out=%v", refused, out)
	}
	if archived, err := h.remote.reg.SessionArchived("remote-a"); err != nil || archived {
		t.Fatalf("archived=%v err=%v, want an archive remote-project-control leaves to the person kept off the paired host", archived, err)
	}
}

func (h hostTargets) pinAsTheCLIDoes(t *testing.T, caller, sessionID string) map[string]any {
	t.Helper()
	target := hosts.Qualify(h.record.ID, sessionID)
	scope := askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: caller, Action: "control", Target: target}, "127.0.0.1:50000")
	if scope.status != http.StatusOK {
		t.Fatalf("the scope check to pin %s: %+v body=%s", sessionID, scope, scope.body)
	}
	response := h.local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{
		"action": "session-pin", "sessionId": target, "sourceSessionId": caller,
		"scopeProjectId": scope.reply.ScopeProjectID, "scopeRank": scope.reply.ScopeRank,
		"scopeOverseen": scope.reply.ScopeOverseen, "scopeControl": scope.reply.ScopeControl,
	})
	var answer map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("pinning %s on the paired host: status=%d body=%s", sessionID, response.Code, response.Body.String())
	}
	return answer
}

func TestAnAgentChangesASessionItStartedOnThisHostAsItsRoleDecides(t *testing.T) {
	f := newMCPWriteFixture(t)
	person := &toldPerson{}
	f.d.notifyDesktop = person.notify
	rename := func(name string) (map[string]any, bool) {
		return callMCP(t, f.parent, "agenthail_alias", map[string]any{"target": "to", "name": name, "clientRequestId": "alias-" + name})
	}

	if out, refused := rename("quiet"); refused {
		t.Fatalf("renaming a session it started: %v", out)
	}
	if told := person.told(); len(told) != 0 {
		t.Fatalf("the person was told %q", told)
	}

	setRoleDecision(t, f.r, agentroles.Worker, "overseen-control", agentroles.Notify)
	if out, refused := rename("told"); refused {
		t.Fatalf("a rename the person hears about: %v", out)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Renamed @quiet") {
		t.Fatalf("with overseen-control notify the person was told %q", told)
	}

	setRoleDecision(t, f.r, agentroles.Worker, "overseen-control", agentroles.Ask)
	out, refused := callMCP(t, f.parent, "agenthail_archive", map[string]any{"target": "to", "clientRequestId": "archive-kept"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	if archived, err := f.r.SessionArchived("to"); err != nil || archived {
		t.Fatalf("archived=%v err=%v, want an archive the role leaves to the person refused", archived, err)
	}
}
