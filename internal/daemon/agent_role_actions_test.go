package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func grantRole(t *testing.T, r *registry.Registry, sessionID, action, decision string) {
	t.Helper()
	role := agentroles.BuiltIn[agentroles.Lead]
	role.Description = ""
	role.Actions = map[string]string{action: decision}
	definition, err := json.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutAgentRole("steward", definition, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutAgentRoleAssignment(registry.AssignSession, sessionID, "steward", false); err != nil {
		t.Fatal(err)
	}
}

func withRequestID(args map[string]any, id string) map[string]any {
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	if _, writes := args["clientRequestId"]; writes {
		out["clientRequestId"] = id
	}
	return out
}

func refusedByRole(out map[string]any, refused bool, action string) bool {
	detail, _ := out["detail"].(map[string]any)
	return refused && detail["action"] == action
}

var forPersonTools = []struct {
	action string
	tool   string
	args   map[string]any
}{
	{"coordinator-any", "agenthail_coordinator", map[string]any{"action": "clear", "project": "x", "clientRequestId": "c"}},
	{"brief", "agenthail_brief", map[string]any{"project": "x", "clientRequestId": "b"}},
	{"history-read", "agenthail_history", map[string]any{"view": "queue"}},
	{"queue-any", "agenthail_queue", map[string]any{"action": "clear", "target": "to", "clientRequestId": "q"}},
	{"relay-any", "agenthail_relay", map[string]any{"action": "list"}},
	{"channel-any", "agenthail_channel", map[string]any{"action": "list"}},
	{"notion-create", "agenthail_create", map[string]any{"surface": "notion", "message": "notes", "clientRequestId": "n"}},
}

func TestEachToolForThePersonFollowsTheCallersRoleNotItsReach(t *testing.T) {
	for _, gate := range forPersonTools {
		t.Run(gate.action, func(t *testing.T) {
			f := newMCPWriteFixture(t)
			server := mcpServer(t, f.d)
			if out, refused := callMCP(t, f.parent, gate.tool, withRequestID(gate.args, "worker")); !refusedByRole(out, refused, gate.action) {
				t.Fatalf("a worker: refused=%v %v", refused, out)
			}
			operator := asOperator(t, f.d, server, "from")
			if out, refused := callMCP(t, operator, gate.tool, withRequestID(gate.args, "operator")); refusedByRole(out, refused, gate.action) {
				t.Fatalf("the built-in operator was refused: %v", out)
			}
			setRoleDecision(t, f.r, agentroles.Operator, gate.action, agentroles.Ask)
			if out, refused := callMCP(t, operator, gate.tool, withRequestID(gate.args, "edited")); !refusedByRole(out, refused, gate.action) {
				t.Fatalf("an operator whose role asks: refused=%v %v", refused, out)
			}

			granted := newMCPWriteFixture(t)
			grantRole(t, granted.r, "to", gate.action, agentroles.Allow)
			if out, refused := callMCP(t, granted.child, gate.tool, withRequestID(gate.args, "granted")); refusedByRole(out, refused, gate.action) {
				t.Fatalf("a role reaching its project that the person let take it was refused: %v", out)
			}

			remote := newRemoteFixture(t, "")
			session := remote.connect(t, map[string]string{"Authorization": "Bearer " + remote.token}, nil)
			if out, refused := callMCP(t, session, gate.tool, withRequestID(gate.args, "remote")); refusedByRole(out, refused, gate.action) {
				t.Fatalf("the built-in remote role was refused: %v", out)
			}
		})
	}
}

func TestARoleThatNotifiesTellsThePersonOnceTheToolHasRun(t *testing.T) {
	f := newMCPWriteFixture(t)
	person := &toldPerson{}
	f.d.notifyDesktop = person.notify
	grantRole(t, f.r, "to", "channel-any", agentroles.Notify)
	if out, refused := callMCP(t, f.child, "agenthail_channel", map[string]any{"action": "create", "channel": "ops", "clientRequestId": "n1"}); refused {
		t.Fatalf("creating a channel: %v", out)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Changed a channel") {
		t.Fatalf("the person was told %q", told)
	}
	if _, refused := callMCP(t, f.child, "agenthail_channel", map[string]any{"action": "rename", "channel": "missing", "newName": "x", "clientRequestId": "n2"}); !refused {
		t.Fatal("renaming a channel that does not exist succeeded")
	}
	if told := person.told(); len(told) != 1 {
		t.Fatalf("a failed change told the person %q", told)
	}

	grantRole(t, f.r, "to", "history-read", agentroles.Notify)
	if _, refused := callMCP(t, f.child, "agenthail_history", map[string]any{"view": "everything"}); !refused {
		t.Fatal("an unknown history view was read")
	}
	if told := person.told(); len(told) != 1 {
		t.Fatalf("a refused read told the person %q", told)
	}
	if out, refused := callMCP(t, f.child, "agenthail_history", map[string]any{"view": "queue"}); refused {
		t.Fatalf("reading the queue: %v", out)
	}
	if told := person.told(); len(told) != 2 || !strings.Contains(told[1], "Read the delivery history") {
		t.Fatalf("the person was told %q", told)
	}
}

func TestAnOperatorWhoseRoleAsksNeitherAnswersAsThePersonNorAddsAlwaysRules(t *testing.T) {
	f := newMCPAnswerFixture(t)
	lead, err := f.r.Session("lead")
	if err != nil {
		t.Fatal(err)
	}
	operator := surface.Session{ID: "operator", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Cwd: lead.Cwd}
	if err := f.r.RecordObservedSession(operator); err != nil {
		t.Fatal(err)
	}
	f.surface.sessions["operator"] = operator
	voice := asOperator(t, f.d, f.server, "operator")
	setRoleDecision(t, f.r, agentroles.Operator, "answer-for-person", agentroles.Ask)

	if out, refused := callMCP(t, voice, "agenthail_answer", map[string]any{"requestId": f.open(t, "worker", "worker-prompt"), "decision": "always", "clientRequestId": "v1"}); !refusedByRole(out, refused, "answer-for-person") {
		t.Fatalf("always-allow from an operator whose role asks: refused=%v %v", refused, out)
	}
	askID := f.open(t, "lead", "lead-prompt")
	if _, refused := callMCP(t, voice, "agenthail_answer", map[string]any{"requestId": askID, "decision": "allow", "clientRequestId": "v2"}); !refused {
		t.Fatal("an operator whose role asks answered a prompt routed to the person")
	}
	if f.answered("lead/lead-prompt") || f.answered("worker/worker-prompt") {
		t.Fatal("a refused answer reached a session")
	}

	setRoleDecision(t, f.r, agentroles.Operator, "answer-for-person", agentroles.Allow)
	if out, refused := callMCP(t, voice, "agenthail_answer", map[string]any{"requestId": askID, "decision": "allow", "clientRequestId": "v3"}); refused {
		t.Fatalf("an operator whose role allows: %v", out)
	}
	if ask, _, _ := f.r.Ask(askID); ask.AnsweredBy != registry.AskToPerson {
		t.Fatalf("ask=%+v, want answered for the person", ask)
	}
}

func TestARoleGivenModeRaiseSetsAModeAboveTheTargetsCeiling(t *testing.T) {
	f := newMCPAnswerFixture(t)
	if _, err := f.r.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES('worker','{"permission":"edits"}')`); err != nil {
		t.Fatal(err)
	}
	raise := func(session *mcp.ClientSession, id string) string {
		return refusalCode(t, session, "agenthail_session_settings", map[string]any{"target": "worker", "setting": "permissionMode", "value": "bypassPermissions", "clientRequestId": id})
	}
	if code := raise(f.lead, "r1"); code != "exceeds_ceiling" {
		t.Fatalf("a spawner without mode-raise: code=%q", code)
	}
	grantRole(t, f.r, "lead", "mode-raise", agentroles.Allow)
	if code := raise(f.lead, "r2"); code != "" {
		t.Fatalf("a spawner whose role may raise: code=%q", code)
	}
}

func TestAnOperatorWhoseRoleAsksIsRefusedAnotherProjectsChangeFromTheCommandLine(t *testing.T) {
	f := newMCPWriteFixture(t)
	asOperator(t, f.d, mcpServer(t, f.d), "from")
	if _, err := f.r.AddProject(registry.ProjectRecord{ID: "p-other", WorkspaceRoot: t.TempDir(), Title: "Other"}, ""); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": "project-archive", "projectId": "p-other"})
	archive := func() scopeAnswer {
		return askScope(t, f.d, daemonapi.ScopeRequest{SessionID: "from", Action: "operator", Payload: &daemonapi.PersonAction{API: daemonapi.PersonActionAPI, Body: body}}, "127.0.0.1:50000")
	}
	if admitted := archive(); admitted.status != http.StatusOK || admitted.reply.Notice != "" {
		t.Fatalf("the built-in operator: %+v", admitted)
	}
	setRoleDecision(t, f.r, agentroles.Operator, "project-any", agentroles.Ask)
	if refused := archive(); refused.code != "capability_missing" {
		t.Fatalf("an operator whose role asks: %+v", refused)
	}
	setRoleDecision(t, f.r, agentroles.Operator, "project-any", agentroles.Notify)
	if noticed := archive(); noticed.status != http.StatusOK || noticed.reply.Notice == "" {
		t.Fatalf("an operator whose role notifies: %+v", noticed)
	}
}

func TestTheFullReachRolesKeepWhatTheyDidBeforeTheirActionsWereNamed(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	want := map[string]string{
		"session-create-remote": agentroles.Notify,
		"extension-build":       agentroles.Allow,
		"work-takeover":         agentroles.Allow,
	}
	for _, action := range agentroles.Actions {
		if action.AllReach == agentroles.Allow && action.Default == agentroles.Ask {
			want[action.Name] = agentroles.Allow
		}
	}
	if len(want) != 14 {
		t.Fatalf("%d actions checked; the full-reach roles took 11 for the person and 3 more before they were named", len(want))
	}
	for _, remote := range []bool{false, true} {
		role := d.callerRole(registry.CallerRecord{Roles: registry.SessionRoles{Operator: true}}, remote)
		for action, decision := range want {
			if got := role.Decision(action); got != decision {
				t.Errorf("%s decides %s %s; before it was %s", role.Name, action, got, decision)
			}
		}
	}
}

func TestAnOperatorStartsABuilderQuietlyUnlessItsRoleAsks(t *testing.T) {
	f := startExtensionFixture(t)
	person := &toldPerson{}
	f.d.notifyDesktop = person.notify
	voice := surface.Session{ID: "voice", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, LastActive: time.Now()}
	if err := f.d.Registry.RecordObservedSession(voice); err != nil {
		t.Fatal(err)
	}
	makeVoiceOperator(t, f.d, voice)
	build := func(message string) (int, string) {
		response, body := f.request(t, http.MethodPost, "/api/v1/extensions/build", "secret", map[string]string{"message": message, "surface": "codex", "sessionId": "voice"})
		return response.StatusCode, string(body)
	}
	if status, body := build("an ideas board"); status != http.StatusCreated || len(person.told()) != 0 {
		t.Fatalf("the built-in operator's build answered %d %s and told %q", status, body, person.told())
	}
	setRoleDecision(t, f.d.Registry, agentroles.Operator, "extension-build", agentroles.Ask)
	if status, body := build("another"); status != http.StatusForbidden || len(f.fake.startOptions) != 1 {
		t.Fatalf("an operator whose role asks answered %d %s with %d starts", status, body, len(f.fake.startOptions))
	}
}

func TestAnOperatorWhoseRoleAsksStartsNothingOnAPairedComputer(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	boundedSpawner(t, local.reg, "voice", "edits")
	voice, err := local.reg.Session("voice")
	if err != nil {
		t.Fatal(err)
	}
	makeVoiceOperator(t, local.daemon, *voice)
	work, err := projectresolve.CanonicalCatalogPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := func() scopeAnswer {
		payload, _ := json.Marshal(map[string]any{"surface": "codex", "host": "vps", "cwd": work, "message": "help"})
		return askScope(t, local.daemon, daemonapi.ScopeRequest{SessionID: "voice", Action: "create", Create: payload}, "127.0.0.1:50000")
	}
	setRoleDecision(t, local.reg, agentroles.Operator, "session-create-remote", agentroles.Ask)
	if refused := start(); refused.rule != "other_host" || len(remote.fake.startOptions) != 0 {
		t.Fatalf("an operator whose role asks: %+v with %d starts", refused, len(remote.fake.startOptions))
	}
	setRoleDecision(t, local.reg, agentroles.Operator, "session-create-remote", agentroles.Notify)
	if started := start(); started.status >= http.StatusMultipleChoices || len(remote.fake.startOptions) != 1 {
		t.Fatalf("the operator's start: %+v with %d starts", started, len(remote.fake.startOptions))
	}
}

func TestAnOperatorTakesOverWorkQuietlyUnlessItsRoleAsks(t *testing.T) {
	f := newMCPWriteFixture(t)
	asOperator(t, f.d, mcpServer(t, f.d), "from")
	takeover := func() scopeAnswer {
		return askScope(t, f.d, daemonapi.ScopeRequest{SessionID: "from", Action: "ledger", Project: "repo:example", Mode: string(ledger.WorkTakeover), Target: "W1"}, "127.0.0.1:50000")
	}
	if quiet := takeover(); quiet.status != http.StatusOK || quiet.reply.Notice != "" {
		t.Fatalf("the built-in operator: %+v %s", quiet, quiet.body)
	}
	setRoleDecision(t, f.r, agentroles.Operator, "work-takeover", agentroles.Ask)
	if refused := takeover(); refused.code != "capability_missing" || refused.rule != "person_only" {
		t.Fatalf("an operator whose role asks: %+v", refused)
	}
}

func TestARoleSeesOnlyTheToolsForThePersonItMayUseAndAListedOneStaysListed(t *testing.T) {
	f := newMCPWriteFixture(t)
	server := mcpServer(t, f.d)
	if tools := listedTools(t, f.parent); slices.Contains(tools, "agenthail_queue") || !slices.Contains(tools, "agenthail_send") {
		t.Fatalf("a worker sees %v", tools)
	}
	if out, refused := callMCP(t, f.parent, "agenthail_queue", map[string]any{"action": "clear", "target": "to", "clientRequestId": "hidden"}); !refusedByRole(out, refused, "queue-any") {
		t.Fatalf("calling a hidden tool: refused=%v %v", refused, out)
	}
	grantRole(t, f.r, "to", "queue-any", agentroles.Notify)
	if tools := listedTools(t, f.child); !slices.Contains(tools, "agenthail_queue") || slices.Contains(tools, "agenthail_relay") {
		t.Fatalf("a role given queue-any sees %v", tools)
	}
	changeRole(t, f.r, agentroles.Worker, func(role *agentroles.Role) {
		role.Tools = []string{"agenthail_relay", "agenthail_whoami"}
	})
	operator := asOperator(t, f.d, server, "from")
	if tools := listedTools(t, operator); !slices.Contains(tools, "agenthail_relay") || !slices.Contains(tools, "agenthail_queue") {
		t.Fatalf("the operator sees %v", tools)
	}
	plain := surface.Session{ID: "plain", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: t.TempDir()}
	if err := f.r.RecordObservedSession(plain); err != nil {
		t.Fatal(err)
	}
	worker := mcpSession(t, server, issueMCPToken(t, f.d, plain))
	if tools := listedTools(t, worker); !slices.Equal(tools, []string{"agenthail_relay", "agenthail_whoami"}) {
		t.Fatalf("a worker whose role lists its tools sees %v", tools)
	}

	every := agentroles.Role{Reach: agentroles.ReachDescendants}
	for _, tool := range agenttools.Tools {
		every.Tools = append(every.Tools, tool.Name)
	}
	if err := f.d.changeAgentRole(t.Context(), daemonapi.CLIActor, daemonapi.AgentRoleRequest{Op: "put", Name: agentroles.Worker, Role: every}); err != nil {
		t.Fatal(err)
	}
	if tools := listedTools(t, worker); !slices.Contains(tools, "agenthail_queue") {
		t.Fatalf("a worker whose role lists every tool sees %v", tools)
	}
}

func TestExplainingARoleNamesTheRoleWhyAndWhatItDecides(t *testing.T) {
	f := newMCPWriteFixture(t)
	grantRole(t, f.r, "to", "queue-any", agentroles.Notify)
	explain := func(target string) (int, daemonapi.AgentRoleExplanation) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/agents?explain="+url.QueryEscape(target), nil)
		recorder := httptest.NewRecorder()
		f.d.serveAgentRoles(recorder, request, daemonapi.CLIActor)
		var explained daemonapi.AgentRoleExplanation
		_ = json.Unmarshal(recorder.Body.Bytes(), &explained)
		return recorder.Code, explained
	}
	status, explained := explain("to")
	if status != http.StatusOK || explained.Role.Name != "steward" || explained.Source != "assigned to this session" || explained.Replaces != agentroles.Worker {
		t.Fatalf("explaining an assigned session: %d %+v", status, explained)
	}
	if explained.Role.Decisions["queue-any"] != agentroles.Notify || !slices.Contains(explained.Role.Tools, "agenthail_queue") {
		t.Fatalf("the explained role decides %v and sees %v", explained.Role.Decisions, explained.Role.Tools)
	}
	if status, explained := explain("from"); status != http.StatusOK || explained.Role.Name != agentroles.Worker || explained.Replaces != "" {
		t.Fatalf("explaining a worker: %d %+v", status, explained)
	}

	remote := newRemoteFixture(t, "")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agents?explain=@scout", nil)
	recorder := httptest.NewRecorder()
	remote.d.serveAgentRoles(recorder, request, daemonapi.CLIActor)
	var identity daemonapi.AgentRoleExplanation
	if err := json.Unmarshal(recorder.Body.Bytes(), &identity); err != nil || identity.Role.Name != agentroles.Remote || identity.Target != "@scout" {
		t.Fatalf("explaining a remote identity: %d %s", recorder.Code, recorder.Body)
	}
}

func TestThePersonsTerminalExplainsASessionsRoleOverTheLocalSocket(t *testing.T) {
	d, reg, _, _ := mcpSocketDaemon(t)
	stop, err := d.startLocalSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	registerClaude(t, reg, "claude-one")
	if status, body := localSocketPost(t, daemonapi.AgentRoleRequest{Op: "assign", Name: agentroles.Lead, Kind: registry.AssignSession, Target: "claude-one"}); status != http.StatusOK {
		t.Fatalf("assign answered %d %s", status, body)
	}
	get := func(target string) (int, string) {
		client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", daemonctl.LocalSocketPath())
		}}}
		response, err := client.Get("http://agenthail/api/v1/agents?explain=" + url.QueryEscape(target))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	status, body := get("claude-one")
	var explained daemonapi.AgentRoleExplanation
	if err := json.Unmarshal([]byte(body), &explained); status != http.StatusOK || err != nil || explained.Role.Name != agentroles.Lead || explained.Source != "assigned to this session" {
		t.Fatalf("explaining an assigned session: %d %s", status, body)
	}
	if status, body := get("nobody-by-that-name"); status != http.StatusNotFound {
		t.Fatalf("explaining an unknown target answered %d %s", status, body)
	}
}
