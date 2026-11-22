package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemonapi"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
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

func setAgentDecision(t *testing.T, action, decision string) {
	t.Helper()
	changeAgentPolicy(t, func(config *dashconfig.Config) error { return config.SetAgentDecision(action, decision) })
}

var defaultAgentRunning = (*dashconfig.AgentPolicy)(nil).RunningLimit()

func changeAgentPolicy(t *testing.T, change func(*dashconfig.Config) error) {
	t.Helper()
	path := dashconfig.Path()
	before, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
			return
		}
		if err := os.WriteFile(path, before, 0o600); err != nil {
			t.Error(err)
		}
	})
	config, err := dashconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := change(&config); err != nil {
		t.Fatal(err)
	}
	if err := dashconfig.Save(config); err != nil {
		t.Fatal(err)
	}
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
	setAgentDecision(t, "session-create-remote", dashconfig.AgentAsk)
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

	setAgentDecision(t, "extension-build", dashconfig.AgentAsk)
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

	setAgentDecision(t, "project-archive", dashconfig.AgentAllow)
	if quiet := archive(lead.ID); quiet.status != http.StatusOK || quiet.reply.Notice != "" {
		t.Fatalf("an allowed archive: %+v", quiet)
	}
	setAgentDecision(t, "project-archive", dashconfig.AgentAsk)
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

	setAgentDecision(t, "cross-project-message", dashconfig.AgentAsk)
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

func TestThePersonsRunningLimitBoundsAnAgentsStarts(t *testing.T) {
	f := newMCPCodexFixture(t)
	spawner, _, root := f.startParent(t)
	changeAgentPolicy(t, func(config *dashconfig.Config) error { return config.SetAgentLimit("running", 2) })
	registerBusyChildren(t, f.registry, spawner, 2)
	payload, _ := json.Marshal(map[string]any{"surface": "codex", "cwd": root, "message": "one more"})
	refused := askScope(t, f.daemon, daemonapi.ScopeRequest{SessionID: spawner, Action: "create", Create: payload}, "127.0.0.1:50000")
	if refused.code != "children_limit" || !strings.Contains(string(refused.body), "2 sessions running") {
		t.Fatalf("a third start under a limit of 2: %+v %s", refused, refused.body)
	}
}
