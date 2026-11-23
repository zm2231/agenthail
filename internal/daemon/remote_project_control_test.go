package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/hosts"
)

func TestALeadControlsItsProjectsSessionOnAPairedHostAndThePersonIsToldOfAllButItsSteers(t *testing.T) {
	h := newHostTargets(t, true)
	person := &toldPerson{}
	h.local.daemon.notifyDesktop = person.notify

	out, refused := callMCP(t, h.caller, "agenthail_steer", map[string]any{"target": "vps::@builder", "message": "turn left", "clientRequestId": "steer-1"})
	if refused || !h.reached("turn left") {
		t.Fatalf("a lead steering its project's session on the paired host: refused=%v out=%v delivered=%q", refused, out, h.remote.fake.delivered())
	}
	if out, refused := h.send(t, "vps::remote-c", "sent now", map[string]any{"mode": "now"}); refused || !h.reached("sent now") {
		t.Fatalf("a lead sending now: refused=%v out=%v", refused, out)
	}
	if out, refused := callMCP(t, h.caller, "agenthail_alias", map[string]any{"target": "vps::remote-c", "name": "tester", "clientRequestId": "alias-1"}); refused {
		t.Fatalf("a lead naming its project's session on the paired host: %v", out)
	}
	if named, err := h.remote.reg.LookupAlias("tester"); err != nil || named != "remote-c" {
		t.Fatalf("the paired host's alias: %q %v", named, err)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Ran alias on") {
		t.Fatalf("the person was told %q", told)
	}

	out, refused = callMCP(t, h.caller, "agenthail_steer", map[string]any{"target": "vps::remote-b", "message": "outside the project", "clientRequestId": "steer-2"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")

	setRoleDecision(t, h.local.reg, agentroles.Lead, "remote-project-control", agentroles.Ask)
	out, refused = callMCP(t, h.caller, "agenthail_alias", map[string]any{"target": "vps::@tester", "name": "kept", "clientRequestId": "alias-2"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	if h.reached("outside the project") {
		t.Fatal("a refused steer reached the paired host")
	}
	if named, err := h.remote.reg.LookupAlias("kept"); err == nil && named != "" {
		t.Fatalf("a refused alias reached the paired host: %q", named)
	}
}

func TestTheCLIsScopeCheckNamesTheLimitsTheOwningHostChecks(t *testing.T) {
	h := newHostTargets(t, true)
	target := hosts.Qualify(h.record.ID, "remote-c")
	answer := askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "control", Target: target}, "127.0.0.1:50000")
	if answer.status != http.StatusOK || answer.reply.TargetID != target || answer.reply.ScopeProjectID != widgetProject || answer.reply.Notice == "" {
		t.Fatalf("a lead's scope check for its project's session on the paired host: %+v body=%s", answer, answer.body)
	}
}

func TestTheOwningHostChecksAForwardedControlActionAgainstItsOwnRecord(t *testing.T) {
	h := newHostTargets(t, true)
	refusedWith := func(sessionID string, fields map[string]any, rule string) {
		t.Helper()
		body := map[string]any{"action": "steer", "sessionId": hosts.Qualify(h.record.ID, sessionID), "message": "forwarded", "sourceSessionId": "local-a"}
		for key, value := range fields {
			body[key] = value
		}
		response := h.local.request(t, http.MethodPost, "/api/v1/actions", body)
		var refusal struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Detail struct {
				Rule string `json:"rule"`
			} `json:"detail"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &refusal)
		if response.Code != http.StatusForbidden || refusal.Error.Code != "out_of_scope" || refusal.Detail.Rule != rule {
			t.Fatalf("%s with %v: status=%d body=%s, want out_of_scope/%s", sessionID, fields, response.Code, response.Body.String(), rule)
		}
	}

	refusedWith("remote-b", map[string]any{"scopeProjectId": widgetProject}, "different_project")
	refusedWith("remote-c", map[string]any{"scopeProjectId": widgetProject, "scopeRank": "edits"}, "exceeds_ceiling")
	if err := h.remote.reg.ArchiveSession("remote-c", time.Now()); err != nil {
		t.Fatal(err)
	}
	refusedWith("remote-c", map[string]any{"scopeProjectId": widgetProject}, "archived")
	if h.reached("forwarded") {
		t.Fatal("a forwarded action the owning host refused reached its session")
	}
}

func TestTheNoticeForASteerOnAPairedHostSaysOnlyWhatThatHostReported(t *testing.T) {
	h := newHostTargets(t, true)
	person := &toldPerson{}
	h.local.daemon.notifyDesktop = person.notify
	setRoleDecision(t, h.local.reg, agentroles.Lead, "overseen-control", agentroles.Notify)
	target := hosts.Qualify(h.record.ID, "remote-c")
	steered, demoted := false, true
	for _, outcome := range []*bool{&steered, &demoted, nil} {
		answer := askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "control", Payload: scopePayload(daemonapi.PersonActionAPI, `{"action":"steer","sessionId":"`+target+`","message":"turn left"}`)}, "127.0.0.1:50000")
		if answer.status != http.StatusOK || answer.reply.Notice == "" {
			t.Fatalf("a lead's scope check for a steer: %+v body=%s", answer, answer.body)
		}
		askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "notice", Notice: answer.reply.Notice, NotSteered: outcome}, "127.0.0.1:50000")
	}
	held := askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "control", Target: target}, "127.0.0.1:50000")
	askScope(t, h.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "notice", Notice: held.reply.Notice, Ran: "goal-set"}, "127.0.0.1:50000")
	told := person.told()
	want := []string{
		"Steered @builder on vps",
		"Sent @builder on vps a message (that computer does not know this session as its lead, so it could not steer)",
		"Sent @builder on vps a steer (that computer runs an older Agenthail, so it may have arrived as a message)",
		"Ran goal-set on @builder on vps",
	}
	if len(told) != len(want) {
		t.Fatalf("the person was told %q", told)
	}
	for i := range want {
		if !strings.Contains(told[i], want[i]) {
			t.Fatalf("notice %d=%q, want %q", i, told[i], want[i])
		}
	}
}
