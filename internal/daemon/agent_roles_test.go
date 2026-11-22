package daemon

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/agentinbox"
	"github.com/zm2231/agenthail/internal/daemonapi"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/projectresolve"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type toldPerson struct {
	mu       sync.Mutex
	messages []string
}

func (p *toldPerson) notify(title, message, _, _, _ string, _ []surface.RequestAction) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if title == "Agent acted" {
		p.messages = append(p.messages, message)
	}
	return nil
}

func (p *toldPerson) told() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.messages...)
}

func changeRole(t *testing.T, r *registry.Registry, name string, change func(*agentroles.Role)) {
	t.Helper()
	role := agentroles.BuiltIn[name]
	records, err := r.AgentRoles()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Name == name && !record.Deleted {
			if err := json.Unmarshal(record.Definition, &role); err != nil {
				t.Fatal(err)
			}
		}
	}
	change(&role)
	definition, err := json.Marshal(role)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.PutAgentRole(name, definition, false); err != nil {
		t.Fatal(err)
	}
}

func setRoleDecision(t *testing.T, r *registry.Registry, name, action, decision string) {
	t.Helper()
	changeRole(t, r, name, func(role *agentroles.Role) {
		if role.Actions == nil {
			role.Actions = map[string]string{}
		}
		role.Actions[action] = decision
	})
}

func boundedSpawner(t *testing.T, r *registry.Registry, id, permission string) {
	t.Helper()
	if err := r.RecordObservedSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, Cwd: t.TempDir(), LastActive: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES(?,?)`, id, `{"permission":"`+permission+`"}`); err != nil {
		t.Fatal(err)
	}
}

func TestAnAgentStartsASessionOnAPairedComputerWithinItsCeilingAndThePersonIsTold(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	person := &toldPerson{}
	local.daemon.notifyDesktop = person.notify
	boundedSpawner(t, local.reg, "spawner", "edits")
	const local127 = "127.0.0.1:50000"
	work, err := projectresolve.CanonicalCatalogPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	above, _ := json.Marshal(map[string]any{"surface": "codex", "host": "vps", "cwd": work, "message": "help", "approvalPolicy": "never"})
	if refused := askScope(t, local.daemon, daemonapi.ScopeRequest{SessionID: "spawner", Action: "create", Create: above}, local127); refused.code != "exceeds_ceiling" {
		t.Fatalf("a start above the spawner's ceiling on the paired computer: %+v", refused)
	}
	asTask, _ := json.Marshal(map[string]any{"surface": "codex", "host": "vps", "cwd": work, "message": "help", "task": true})
	if refused := askScope(t, local.daemon, daemonapi.ScopeRequest{SessionID: "spawner", Action: "create", Create: asTask}, local127); refused.code != "invalid" {
		t.Fatalf("a task on the paired computer: %+v", refused)
	}
	payload, _ := json.Marshal(map[string]any{"surface": "codex", "host": "vps", "cwd": work + "/", "message": "help"})
	answer := askScope(t, local.daemon, daemonapi.ScopeRequest{SessionID: "spawner", Action: "create", Create: payload}, local127)
	if answer.status >= http.StatusMultipleChoices {
		t.Fatalf("the start on the paired computer: %d %s", answer.status, answer.body)
	}
	if len(remote.fake.startOptions) != 1 || len(local.fake.startOptions) != 0 {
		t.Fatalf("remote starts=%d local starts=%d", len(remote.fake.startOptions), len(local.fake.startOptions))
	}
	if started := remote.fake.startOptions[0]; started.Cwd != work || started.ApprovalPolicy != "on-request" {
		t.Fatalf("the paired computer started %+v, want %s at the spawner's on-request", started, work)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Started a codex session on vps in "+work) {
		t.Fatalf("the person was told %q", told)
	}
}

func TestWhileThePersonKeepsPairedStartsAnAgentIsRefusedAndNothingStarts(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	person := &toldPerson{}
	local.daemon.notifyDesktop = person.notify
	setRoleDecision(t, local.reg, agentroles.Worker, "session-create-remote", agentroles.Ask)
	boundedSpawner(t, local.reg, "spawner", "edits")
	payload, _ := json.Marshal(map[string]any{"surface": "codex", "host": "vps", "cwd": "/srv/work", "message": "help"})
	refused := askScope(t, local.daemon, daemonapi.ScopeRequest{SessionID: "spawner", Action: "create", Create: payload}, "127.0.0.1:50000")
	if refused.code != "out_of_scope" || refused.rule != "other_host" {
		t.Fatalf("a start the person keeps for themselves: %+v", refused)
	}
	if len(remote.fake.startOptions) != 0 || len(person.told()) != 0 {
		t.Fatalf("remote starts=%d told=%q", len(remote.fake.startOptions), person.told())
	}
}

func TestAnAgentStartsAnExtensionBuilderInThePersonsModeAndThePersonIsTold(t *testing.T) {
	f := startExtensionFixture(t)
	person := &toldPerson{}
	f.d.notifyDesktop = person.notify
	if err := f.d.Registry.RecordObservedSession(surface.Session{ID: "lead", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, LastActive: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.Registry.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES('lead','{"permission":"ask"}')`); err != nil {
		t.Fatal(err)
	}
	response, body := f.request(t, http.MethodPost, "/api/v1/extensions/build", "secret", map[string]string{"message": "an ideas board", "surface": "codex", "sessionId": "lead"})
	if response.StatusCode != http.StatusCreated || len(f.fake.startOptions) != 1 {
		t.Fatalf("build answered %d %s with %d starts", response.StatusCode, body, len(f.fake.startOptions))
	}
	if started := f.fake.startOptions[0]; started.ApprovalPolicy != "" || started.PermissionMode != "" {
		t.Fatalf("the builder started in %+v, not the person's own mode", started)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Started an extension builder: an ideas board") {
		t.Fatalf("the person was told %q", told)
	}

	setRoleDecision(t, f.d.Registry, agentroles.Worker, "extension-build", agentroles.Ask)
	response, body = f.request(t, http.MethodPost, "/api/v1/extensions/build", "secret", map[string]string{"message": "another", "surface": "codex", "sessionId": "lead"})
	if response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "person_only") || len(f.fake.startOptions) != 1 {
		t.Fatalf("a build the person keeps for themselves answered %d %s with %d starts", response.StatusCode, body, len(f.fake.startOptions))
	}
}

func TestALeadsActionThePersonHearsAboutIsToldOnceAndOnlyWhenItsCommandReportsIt(t *testing.T) {
	d, r, fake, worker, lead := daemonFixture(t)
	person := &toldPerson{}
	d.notifyDesktop = person.notify
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	worker.Cwd, lead.Cwd = widget, widget
	for _, session := range []surface.Session{worker, lead} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(t.Context())
	found, err := broadcast.FindProject(r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	project := found.ID
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: project, ProjectRoot: widget, Name: "widget-lead", Surface: string(surface.KindCodex), Cwd: widget}, registry.CoordinatorChange{SessionID: lead.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	const local = "127.0.0.1:50000"
	archive := func(caller string) scopeAnswer {
		body, _ := json.Marshal(map[string]any{"action": "project-archive", "projectId": project})
		return askScope(t, d, daemonapi.ScopeRequest{SessionID: caller, Action: "operator", Payload: &daemonapi.PersonAction{API: daemonapi.PersonActionAPI, Body: body}}, local)
	}
	if refused := archive(worker.ID); refused.code != "capability_missing" || refused.rule != "person_only" {
		t.Fatalf("a worker archiving its project: %+v", refused)
	}
	admitted := archive(lead.ID)
	if admitted.status != http.StatusOK || admitted.reply.Notice == "" || len(person.told()) != 0 {
		t.Fatalf("the lead archiving its project: %+v told=%q", admitted, person.told())
	}
	askScope(t, d, daemonapi.ScopeRequest{SessionID: worker.ID, Action: "notice", Notice: admitted.reply.Notice}, local)
	if len(person.told()) != 0 {
		t.Fatalf("another session reported the lead's action: %q", person.told())
	}
	for range 2 {
		askScope(t, d, daemonapi.ScopeRequest{SessionID: lead.ID, Action: "notice", Notice: admitted.reply.Notice}, local)
	}
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "Archive project widget") {
		t.Fatalf("the person was told %q", told)
	}

	setRoleDecision(t, r, agentroles.Lead, "project-archive", agentroles.Allow)
	if quiet := archive(lead.ID); quiet.status != http.StatusOK || quiet.reply.Notice != "" {
		t.Fatalf("an allowed archive: %+v", quiet)
	}
	setRoleDecision(t, r, agentroles.Lead, "project-archive", agentroles.Ask)
	if refused := archive(lead.ID); refused.code != "capability_missing" {
		t.Fatalf("an archive the person keeps for themselves: %+v", refused)
	}
}

func TestAnAgentMessagesAnotherProjectFreelyAndThePersonHearsWhenItSteersOne(t *testing.T) {
	d, r, fake, worker, _ := daemonFixture(t)
	person := &toldPerson{}
	d.notifyDesktop = person.notify
	worker.Cwd = widgetRepo(t, "https://github.com/example/widget.git")
	outsider := surface.Session{ID: "outsider", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: widgetRepo(t, "https://github.com/example/gadget.git")}
	for _, session := range []surface.Session{worker, outsider} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(t.Context())
	const local = "127.0.0.1:50000"
	queued := daemonapi.ScopeRequest{SessionID: worker.ID, Action: "send", Target: outsider.ID}
	if admitted := askScope(t, d, queued, local); admitted.status != http.StatusOK || admitted.reply.TargetID != outsider.ID || len(person.told()) != 0 {
		t.Fatalf("a queued message to another project: %+v told=%q", admitted, person.told())
	}
	now := daemonapi.ScopeRequest{SessionID: worker.ID, Action: "send", Target: outsider.ID, Mode: "now"}
	admitted := askScope(t, d, now, local)
	if admitted.status != http.StatusOK || admitted.reply.Notice == "" || len(person.told()) != 0 {
		t.Fatalf("a message sent now to another project, before it ran: %+v told=%q", admitted, person.told())
	}
	askScope(t, d, daemonapi.ScopeRequest{SessionID: worker.ID, Action: "notice", Notice: admitted.reply.Notice}, local)
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "in another project") {
		t.Fatalf("the person was told %q", told)
	}
	if refused := askScope(t, d, daemonapi.ScopeRequest{SessionID: worker.ID, Action: "answer", Target: outsider.ID, Mode: "allow"}, local); refused.code != "out_of_scope" {
		t.Fatalf("answering for a session in another project: %+v", refused)
	}

	setRoleDecision(t, r, agentroles.Worker, "cross-project-message", agentroles.Ask)
	if refused := askScope(t, d, queued, local); refused.code != "out_of_scope" {
		t.Fatalf("a queued message the person keeps for themselves: %+v", refused)
	}
}

func TestALeadTurnsResumingAfterALimitOffForItsOwnProjectAndThePersonIsTold(t *testing.T) {
	d, r, fake, worker, lead := daemonFixture(t)
	person := &toldPerson{}
	d.notifyDesktop = person.notify
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	worker.Cwd, lead.Cwd = widget, widget
	for _, session := range []surface.Session{worker, lead} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(t.Context())
	found, err := broadcast.FindProject(r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: found.ID, ProjectRoot: widget, Name: "widget-lead", Surface: string(surface.KindCodex), Cwd: widget}, registry.CoordinatorChange{SessionID: lead.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	const local = "127.0.0.1:50000"
	hostWide, _ := json.Marshal(map[string]any{"action": "resume-after-limit", "resumeAfterLimit": false})
	if refused := askScope(t, d, daemonapi.ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: &daemonapi.PersonAction{API: daemonapi.PersonActionSettings, Body: hostWide}}, local); refused.code != "capability_missing" {
		t.Fatalf("a lead turning resuming off for the whole host: %+v", refused)
	}
	ownProject, _ := json.Marshal(map[string]any{"action": "project-setting", "projectId": found.ID, "setting": registry.ProjectSettingLimitResume, "value": registry.ProjectLimitResumeOff})
	admitted := askScope(t, d, daemonapi.ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: &daemonapi.PersonAction{API: daemonapi.PersonActionAPI, Body: ownProject}}, local)
	if admitted.status != http.StatusOK || admitted.reply.Notice == "" {
		t.Fatalf("a lead turning resuming off for its own project: %+v", admitted)
	}
	askScope(t, d, daemonapi.ScopeRequest{SessionID: lead.ID, Action: "notice", Notice: admitted.reply.Notice}, local)
	if told := person.told(); len(told) != 1 || !strings.Contains(told[0], "limit-resume to off") {
		t.Fatalf("the person was told %q", told)
	}
}

func TestARolesRunningLimitBoundsAnAgentsStarts(t *testing.T) {
	f := newMCPCodexFixture(t)
	spawner, _, root := f.startParent(t)
	changeRole(t, f.registry, agentroles.Worker, func(role *agentroles.Role) { role.Running = 2 })
	registerBusyChildren(t, f.registry, spawner, 2)
	payload, _ := json.Marshal(map[string]any{"surface": "codex", "cwd": root, "message": "one more"})
	refused := askScope(t, f.daemon, daemonapi.ScopeRequest{SessionID: spawner, Action: "create", Create: payload}, "127.0.0.1:50000")
	if refused.code != "children_limit" || !strings.Contains(string(refused.body), "2 sessions running") {
		t.Fatalf("a third start under a limit of 2: %+v %s", refused, refused.body)
	}
}

var capabilitiesBeforeRoles = map[string][]string{
	agentroles.Worker:   {"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write", "browser.drive", "simulator.drive"},
	agentroles.Lead:     {"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write", "browser.drive", "simulator.drive", "control.project"},
	agentroles.Operator: {"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write", "browser.drive", "simulator.drive", "control.all", "read.all"},
}

// decisionsBeforeRoles is what one agent policy decided for every agent: a
// worker asked for whatever only a lead could do on its own project.
var decisionsBeforeRoles = map[string][2]string{
	"session-create-remote": {agentroles.Notify, agentroles.Notify},
	"extension-build":       {agentroles.Notify, agentroles.Notify},
	"project-add":           {agentroles.Ask, agentroles.Allow},
	"project-rename":        {agentroles.Ask, agentroles.Allow},
	"project-link":          {agentroles.Ask, agentroles.Allow},
	"project-unlink":        {agentroles.Ask, agentroles.Allow},
	"project-setting":       {agentroles.Ask, agentroles.Allow},
	"project-archive":       {agentroles.Ask, agentroles.Notify},
	"project-unarchive":     {agentroles.Ask, agentroles.Allow},
	"project-remove":        {agentroles.Ask, agentroles.Notify},
	"coordinator-set":       {agentroles.Ask, agentroles.Allow},
	"coordinator-new":       {agentroles.Ask, agentroles.Allow},
	"coordinator-clear":     {agentroles.Ask, agentroles.Notify},
	"relay-add":             {agentroles.Ask, agentroles.Allow},
	"relay-remove":          {agentroles.Ask, agentroles.Allow},
	"queue-cancel":          {agentroles.Ask, agentroles.Allow},
	"queue-clear":           {agentroles.Ask, agentroles.Allow},
	"channel-create":        {agentroles.Ask, agentroles.Allow},
	"channel-rename":        {agentroles.Ask, agentroles.Allow},
	"channel-add":           {agentroles.Ask, agentroles.Allow},
	"channel-remove":        {agentroles.Ask, agentroles.Allow},
	"channel-delete":        {agentroles.Ask, agentroles.Notify},
	"limit-resume":          {agentroles.Ask, agentroles.Notify},
	"cross-project-message": {agentroles.Allow, agentroles.Allow},
	"cross-project-steer":   {agentroles.Notify, agentroles.Notify},
}

func listedTools(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestTheBuiltInRolesGiveEveryAgentTheAccessItHadBeforeRoles(t *testing.T) {
	d, r, fake, worker, lead := daemonFixture(t)
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	worker.Cwd, lead.Cwd = widget, widget
	voice := surface.Session{ID: "voice", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: widget}
	for _, session := range []surface.Session{worker, lead, voice} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(t.Context())
	found, err := broadcast.FindProject(r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: found.ID, ProjectRoot: widget, Name: "widget-lead", Surface: string(surface.KindCodex), Cwd: widget}, registry.CoordinatorChange{SessionID: lead.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	sessions := map[string]*mcp.ClientSession{
		agentroles.Worker:   mcpSession(t, server, issueMCPToken(t, d, worker)),
		agentroles.Lead:     mcpSession(t, server, issueMCPToken(t, d, lead)),
		agentroles.Operator: asOperator(t, d, server, voice.ID),
	}
	var everyTool []string
	for _, tool := range agenttools.Tools {
		everyTool = append(everyTool, tool.Name)
	}
	slices.Sort(everyTool)
	for name, session := range sessions {
		whoami, refused := callMCP(t, session, "agenthail_whoami", nil)
		if refused {
			t.Fatalf("%s whoami: %v", name, whoami)
		}
		var capabilities []string
		for _, capability := range whoami["capabilities"].([]any) {
			capabilities = append(capabilities, capability.(string))
		}
		if !slices.Equal(capabilities, capabilitiesBeforeRoles[name]) {
			t.Errorf("a %s holds %v; before roles it held %v", name, capabilities, capabilitiesBeforeRoles[name])
		}
		if role, _ := whoami["role"].(map[string]any); role["name"] != name {
			t.Errorf("a %s's whoami names its role %v", name, whoami["role"])
		}
		if tools := listedTools(t, session); !slices.Equal(tools, everyTool) {
			t.Errorf("a %s sees %v; before roles every agent saw %v", name, tools, everyTool)
		}
	}

	for column, flags := range []registry.SessionRoles{{}, {Lead: true}} {
		role := d.callerRole(registry.CallerRecord{Roles: flags}, false)
		for action, before := range decisionsBeforeRoles {
			if got := role.Decision(action); got != before[column] {
				t.Errorf("%s decides %s %s; before roles it was %s", role.Name, action, got, before[column])
			}
		}
	}
	if len(decisionsBeforeRoles) != len(agentroles.Actions) {
		t.Errorf("%d actions before roles, %d now", len(decisionsBeforeRoles), len(agentroles.Actions))
	}
	for _, flags := range []registry.SessionRoles{{}, {Lead: true}, {Operator: true}} {
		for _, remote := range []bool{false, true} {
			role := d.callerRole(registry.CallerRecord{Roles: flags}, remote)
			if role.DepthLimit() != 3 || role.RunningLimit() != 8 {
				t.Errorf("%s nests %d deep and runs %d; before roles every agent had 3 and 8", role.Name, role.DepthLimit(), role.RunningLimit())
			}
		}
	}
}

func TestARemoteIdentityKeepsThePersonsReachUnderTheRemoteRole(t *testing.T) {
	f := newRemoteFixture(t, "")
	whoami, refused := callMCP(t, f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil), "agenthail_whoami", nil)
	if refused {
		t.Fatalf("whoami: %v", whoami)
	}
	var capabilities []string
	for _, capability := range whoami["capabilities"].([]any) {
		capabilities = append(capabilities, capability.(string))
	}
	if !slices.Equal(capabilities, capabilitiesBeforeRoles[agentroles.Operator]) {
		t.Fatalf("a remote identity holds %v; before roles it held the operator's %v", capabilities, capabilitiesBeforeRoles[agentroles.Operator])
	}
	if role, _ := whoami["role"].(map[string]any); role["name"] != agentroles.Remote || role["reach"] != agentroles.ReachAll {
		t.Fatalf("a remote identity's role is %v", whoami["role"])
	}
}

func TestARoleThatHidesAToolLeavesItOutOfTheListAndRefusesIt(t *testing.T) {
	d, r, _, worker, _ := daemonFixture(t)
	changeRole(t, r, agentroles.Worker, func(role *agentroles.Role) {
		for _, tool := range agenttools.Tools {
			if tool.Name != "agenthail_sessions" {
				role.Tools = append(role.Tools, tool.Name)
			}
		}
	})
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, worker))
	if tools := listedTools(t, session); slices.Contains(tools, "agenthail_sessions") || !slices.Contains(tools, "agenthail_whoami") {
		t.Fatalf("a worker without the session list sees %v", tools)
	}
	out, refused := callMCP(t, session, "agenthail_sessions", nil)
	if detail, _ := out["detail"].(map[string]any); !refused || detail["rule"] != "role_tools" {
		t.Fatalf("calling a tool the role hides: refused=%v %v", refused, out)
	}
}

func TestAPersonsRolesAndAssignmentsReachEveryPairedHostAndResolveAlikeThere(t *testing.T) {
	laptop, server := linkedFederation(t)
	var told []string
	laptop.daemon.notifyDesktop = func(title, message, _, _, _ string, _ []surface.RequestAction) error {
		if title == "Agent role changed" {
			told = append(told, message)
		}
		return nil
	}
	put := AgentRoleRequest{Op: "put", Name: agentroles.Lead, Role: agentroles.Role{Reach: agentroles.ReachProject, Actions: map[string]string{"project-archive": agentroles.Ask}, Running: 4}}
	if response := laptop.request(t, http.MethodPost, "/api/v1/agents", put); response.Code != http.StatusForbidden {
		t.Fatalf("a write with the dashboard token, which any local process can read, answered %d %s", response.Code, response.Body)
	}
	change := func(node *federationNode, request AgentRoleRequest) {
		t.Helper()
		if err := node.daemon.changeAgentRole(context.Background(), daemonapi.CLIActor, request); err != nil {
			t.Fatal(err)
		}
	}
	change(laptop, put)
	if len(told) != 1 || told[0] != "the CLI changed the lead role." {
		t.Fatalf("the person was told %q", told)
	}
	leadOn := func(node *federationNode) resolvedRole {
		return node.daemon.callerRole(registry.CallerRecord{Roles: registry.SessionRoles{Lead: true}}, false)
	}
	testkit.Eventually(t, "the server takes the laptop's lead role", 10*time.Second, func() bool {
		role := leadOn(server)
		return role.Decision("project-archive") == agentroles.Ask && role.RunningLimit() == 4
	})
	change(laptop, AgentRoleRequest{Op: "put", Name: "reviewer", Role: agentroles.Role{Reach: agentroles.ReachDescendants}})
	change(laptop, AgentRoleRequest{Op: "assign", Name: "reviewer", Kind: registry.AssignAlias, Target: "@builder"})
	builderOn := func(node *federationNode) resolvedRole {
		return node.daemon.callerRole(registry.CallerRecord{Alias: "builder", Roles: registry.SessionRoles{Lead: true}}, false)
	}
	testkit.Eventually(t, "the server gives @builder the laptop's assignment", 10*time.Second, func() bool {
		role := builderOn(server)
		return role.Name == "reviewer" && role.label() == "lead (replaced by reviewer)"
	})
	change(server, AgentRoleRequest{Op: "delete", Name: "reviewer"})
	for _, node := range []*federationNode{server, laptop} {
		testkit.Eventually(t, "an assignment to a deleted role is passed over on every host", 10*time.Second, func() bool {
			view, err := node.daemon.agentRolesView()
			return err == nil && builderOn(node).Name == agentroles.Lead && len(view.Assignments) == 1 && view.Assignments[0].Stale == "no role reviewer"
		})
	}
	change(server, AgentRoleRequest{Op: "delete", Name: agentroles.Lead})
	testkit.Eventually(t, "the laptop takes the server's reset", 10*time.Second, func() bool {
		role := leadOn(laptop)
		return role.Decision("project-archive") == agentroles.Notify && role.RunningLimit() == 8 && !role.Edited
	})
}

func TestAnAssignmentGoesToTheSessionItsBranchesItsIdentityThenItsAlias(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	for _, session := range []surface.Session{{ID: "parent"}, {ID: "child", ForkedFrom: "parent"}} {
		session.Surface, session.RuntimeKind, session.Status = surface.KindClaude, surface.RuntimeClaudeCLI, surface.StatusIdle
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	change := func(request AgentRoleRequest) {
		t.Helper()
		if err := d.changeAgentRole(context.Background(), daemonapi.CLIActor, request); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"by-alias", "by-identity", "by-parent", "by-session"} {
		change(AgentRoleRequest{Op: "put", Name: name, Role: agentroles.Role{Reach: agentroles.ReachDescendants}})
	}
	caller := registry.CallerRecord{Session: surface.Session{ID: "child"}, Identity: "scout", Alias: "scout", Roles: registry.SessionRoles{Lead: true}}
	holds := func(want, label string) {
		t.Helper()
		role := d.callerRole(caller, false)
		if role.Name != want || role.label() != label {
			t.Fatalf("the caller holds %s (%q, %s), want %s (%q)", role.Name, role.label(), role.Source, want, label)
		}
	}
	holds(agentroles.Lead, agentroles.Lead)
	change(AgentRoleRequest{Op: "assign", Name: "by-alias", Kind: registry.AssignAlias, Target: "scout"})
	holds("by-alias", "lead (replaced by by-alias)")
	change(AgentRoleRequest{Op: "assign", Name: "by-identity", Kind: registry.AssignIdentity, Target: "scout"})
	holds("by-identity", "lead (replaced by by-identity)")
	change(AgentRoleRequest{Op: "assign", Name: "by-parent", Kind: registry.AssignSession, Target: "parent"})
	holds("by-parent", "lead (replaced by by-parent)")
	change(AgentRoleRequest{Op: "assign", Name: "by-session", Kind: registry.AssignSession, Target: "child"})
	holds("by-session", "lead (replaced by by-session)")
	change(AgentRoleRequest{Op: "delete", Name: "by-session"})
	holds("by-parent", "lead (replaced by by-parent)")
	change(AgentRoleRequest{Op: "unassign", Kind: registry.AssignSession, Target: "parent"})
	holds("by-identity", "lead (replaced by by-identity)")
	if err := r.ArchiveSession("parent", time.Now()); err != nil {
		t.Fatal(err)
	}
	change(AgentRoleRequest{Op: "assign", Name: "by-alias", Kind: registry.AssignSession, Target: "parent"})
	view, err := d.agentRolesView()
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]string{}
	for _, assignment := range view.Assignments {
		stale[assignment.Kind+":"+assignment.Target] = assignment.Stale
	}
	if stale["session:child"] != "no role by-session" || stale["session:parent"] != "the session is archived" || stale["identity:scout"] != "" {
		t.Fatalf("the assignments read as %v", stale)
	}
}

func TestThePersonsStoredAgentPolicyBecomesTheRolesItAppliedTo(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(strings.TrimSuffix(dashconfig.Path(), "/dashboard.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	stored := `{"listen":"127.0.0.1:7777","agents":{"actions":{"project-archive":"ask","cross-project-steer":"allow"},"running":5}}`
	if err := os.WriteFile(dashconfig.Path(), []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	var told []string
	d.notifyDesktop = func(title, message, _, _, _ string, _ []surface.RequestAction) error {
		told = append(told, message)
		return nil
	}
	if err := d.foldAgentPolicy(); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 || told[0] != "Agenthail moved this computer's agent policy into the worker, lead, operator, remote roles." {
		t.Errorf("the person was told %q", told)
	}
	for _, flags := range []registry.SessionRoles{{}, {Lead: true}, {Operator: true}} {
		role := d.callerRole(registry.CallerRecord{Roles: flags}, false)
		if role.Decision("cross-project-steer") != agentroles.Allow || role.RunningLimit() != 5 {
			t.Errorf("%s steers %s and runs %d", role.Name, role.Decision("cross-project-steer"), role.RunningLimit())
		}
	}
	if lead := d.callerRole(registry.CallerRecord{Roles: registry.SessionRoles{Lead: true}}, false); lead.Decision("project-archive") != agentroles.Ask {
		t.Errorf("the lead archives with %s", lead.Decision("project-archive"))
	}
	if worker := d.callerRole(registry.CallerRecord{Roles: registry.SessionRoles{}}, false); worker.Actions["project-archive"] != "" {
		t.Errorf("a worker took a lead's choice: %v", worker.Actions)
	}
	data, err := os.ReadFile(dashconfig.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"agents"`) || !strings.Contains(string(data), "127.0.0.1:7777") {
		t.Fatalf("dashboard.json after the fold: %s", data)
	}
	records, err := r.AgentRoles()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.foldAgentPolicy(); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 {
		t.Errorf("a second start told the person again: %q", told)
	}
	again, err := r.AgentRoles()
	if err != nil || !slices.EqualFunc(records, again, func(a, b registry.AgentRoleRecord) bool { return a.ChangedAt.Equal(b.ChangedAt) }) {
		t.Fatalf("a second start folded again: %v", err)
	}
}

func TestASessionsDetailNamesTheRoleItActsUnderAndWhatItReplaced(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := dashboardRouter(d)
	role := func(id string) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id="+id, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var body struct {
			Role map[string]any `json:"role"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("session detail answered %d %s", response.Code, response.Body)
		}
		return body.Role
	}
	if got := role("from"); got["name"] != agentroles.Worker || got["label"] != agentroles.Worker || got["replaces"] != nil {
		t.Fatalf("an unassigned session's role reads %v", got)
	}
	for _, request := range []AgentRoleRequest{
		{Op: "put", Name: "reviewer", Role: agentroles.Role{Reach: agentroles.ReachProject}},
		{Op: "assign", Name: "reviewer", Kind: registry.AssignSession, Target: "from"},
	} {
		if err := d.changeAgentRole(context.Background(), daemonapi.CLIActor, request); err != nil {
			t.Fatal(err)
		}
	}
	if got := role("from"); got["name"] != "reviewer" || got["reach"] != agentroles.ReachProject || got["replaces"] != agentroles.Worker || got["label"] != "worker (replaced by reviewer)" || got["source"] != "assigned to this session" {
		t.Fatalf("an assigned session's role reads %v", got)
	}
}

func TestARemoteIdentitysDetailShowsTheRoleARemoteCallerActsUnderAsWhoamiDoes(t *testing.T) {
	f := newRemoteFixture(t, "")
	f.d.Surfaces = append(f.d.Surfaces, agentinbox.New(func() *registry.Registry { return f.r }))
	handler := dashboardRouter(f.d)
	detail := func() (map[string]any, map[string]any) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id="+f.scout.SessionID, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var body struct {
			Role       map[string]any `json:"role"`
			RemoteRole map[string]any `json:"remoteRole"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("session detail answered %d %s", response.Code, response.Body)
		}
		return body.Role, body.RemoteRole
	}
	whoami := func() map[string]any {
		t.Helper()
		out, refused := callMCP(t, f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil), "agenthail_whoami", nil)
		if refused {
			t.Fatalf("whoami: %v", out)
		}
		role, _ := out["role"].(map[string]any)
		return role
	}
	agree := func(want string) {
		t.Helper()
		local, remote := detail()
		reached := whoami()
		if remote["name"] != want || !maps.Equal(remote, reached) {
			t.Fatalf("the detail's remote role %v, whoami from elsewhere %v, want %s", remote, reached, want)
		}
		if local["name"] == agentroles.Remote {
			t.Fatalf("the session's own role reads %v", local)
		}
	}
	agree(agentroles.Remote)
	for _, request := range []AgentRoleRequest{
		{Op: "put", Name: "reviewer", Role: agentroles.Role{Reach: agentroles.ReachProject}},
		{Op: "assign", Name: "reviewer", Kind: registry.AssignIdentity, Target: "scout"},
	} {
		if err := f.d.changeAgentRole(context.Background(), daemonapi.CLIActor, request); err != nil {
			t.Fatal(err)
		}
	}
	agree("reviewer")
	if _, remote := detail(); remote["label"] != "remote (replaced by reviewer)" {
		t.Fatalf("the remote role's label reads %v", remote["label"])
	}
}
