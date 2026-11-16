package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	widgetProject = "project-widget"
	gadgetProject = "project-gadget"
)

type gatedFixture struct {
	t      *testing.T
	app    *App
	r      *registry.Registry
	widget string
}

func newGatedFixture(t *testing.T) gatedFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	widget, gadget := mustEvalSymlinks(t, t.TempDir()), mustEvalSymlinks(t, t.TempDir())
	session := func(id, cwd string, status surface.SessionStatus) surface.Session {
		return surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Name: id, Cwd: cwd, Status: status, LastActive: time.Now()}
	}
	sessions := map[string]surface.Session{
		"worker":       session("worker", widget, surface.StatusBusy),
		"widget-lead":  session("widget-lead", widget, surface.StatusBusy),
		"gadget-lead":  session("gadget-lead", gadget, surface.StatusBusy),
		"widget-other": session("widget-other", widget, surface.StatusBusy),
		"gadget-other": session("gadget-other", gadget, surface.StatusBusy),
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: sessions, caps: surface.Capabilities{Send: true, Steer: true, Interrupt: true}}
	app, r := cliFixture(t, fake)
	app.scopeOverride = nil
	for _, s := range sessions {
		if err := r.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, lead := range []struct{ project, root, session string }{{widgetProject, widget, "widget-lead"}, {gadgetProject, gadget, "gadget-lead"}} {
		if err := r.AddProject(registry.ProjectRecord{ID: lead.project, WorkspaceRoot: lead.root, Title: strings.TrimPrefix(lead.project, "project-"), Added: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: lead.project, ProjectRoot: lead.root, Name: lead.session, Surface: string(surface.KindCodex), Cwd: lead.root}, registry.CoordinatorChange{SessionID: lead.session, ChangedBy: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	serveAppDaemon(t, app, r, app.allSurfaces())
	return gatedFixture{t: t, app: app, r: r, widget: widget}
}

func (f gatedFixture) folder() string {
	return mustEvalSymlinks(f.t, f.t.TempDir())
}

func (f gatedFixture) addedFolder() string {
	folder := f.folder()
	if err := f.r.AddProject(registry.ProjectRecord{ID: "project-" + filepath.Base(folder), WorkspaceRoot: folder, Title: "folder " + filepath.Base(folder), Added: true}); err != nil {
		f.t.Fatal(err)
	}
	return folder
}

func (f gatedFixture) linkedFolder() string {
	folder := f.addedFolder()
	if _, err := f.r.SetProjectLink("project-"+filepath.Base(folder), widgetProject, true); err != nil {
		f.t.Fatal(err)
	}
	return folder
}

func (f gatedFixture) channel(member string) string {
	name := "ch" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := f.r.CreateChannel(name); err != nil {
		f.t.Fatal(err)
	}
	if member != "" {
		if err := f.r.AddToChannel(name, member); err != nil {
			f.t.Fatal(err)
		}
	}
	return name
}

func (f gatedFixture) relay() string {
	id, err := f.r.AddRoute("worker", "widget-other", ".*")
	if err != nil {
		f.t.Fatal(err)
	}
	return strconv.FormatInt(id, 10)
}

func (f gatedFixture) queued() string {
	id, err := f.r.QueueMessageWithOptions("widget-other", "later", "", surface.SendOptions{SourceSessionID: "worker"})
	if err != nil {
		f.t.Fatal(err)
	}
	return strconv.FormatInt(id, 10)
}

const (
	refused = "refused"
	runs    = "runs"
)

var gatedCommands = []struct {
	action                     string
	args                       func(f gatedFixture) []string
	worker, ownLead, otherLead string
}{
	{"project-add", func(f gatedFixture) []string { return []string{"project", "add", f.folder()} }, refused, runs, runs},
	{"project-remove", func(gatedFixture) []string { return []string{"project", "remove", widgetProject} }, refused, refused, refused},
	{"project-rename", func(gatedFixture) []string { return []string{"project", "rename", widgetProject, "Widgets"} }, refused, runs, refused},
	{"project-archive", func(gatedFixture) []string { return []string{"project", "archive", widgetProject} }, refused, refused, refused},
	{"project-unarchive", func(gatedFixture) []string { return []string{"project", "unarchive", widgetProject} }, refused, refused, refused},
	{"project-link", func(f gatedFixture) []string { return []string{"project", "link", f.addedFolder(), widgetProject} }, refused, runs, refused},
	{"project-unlink", func(f gatedFixture) []string { return []string{"project", "unlink", f.linkedFolder()} }, refused, runs, refused},
	{"project-setting", func(gatedFixture) []string { return []string{"project", "mcp", widgetProject, "off"} }, refused, refused, refused},
	{"coordinator-set", func(f gatedFixture) []string {
		return []string{"coordinator", "set", "widget-other", "--cwd", f.widget}
	}, refused, refused, refused},
	{"coordinator-new", func(f gatedFixture) []string { return []string{"coordinator", "new", "codex", "--cwd", f.widget} }, refused, refused, refused},
	{"coordinator-clear", func(f gatedFixture) []string { return []string{"coordinator", "clear", "--cwd", f.widget} }, refused, refused, refused},
	{"resume-after-limit", func(gatedFixture) []string { return []string{"limit-resume", "off"} }, refused, refused, refused},
	{"relay-add", func(gatedFixture) []string { return []string{"relay", "add", "worker", "widget-other"} }, refused, runs, refused},
	{"relay-remove", func(f gatedFixture) []string { return []string{"relay", "rm", f.relay()} }, refused, runs, refused},
	{"channel-create", func(f gatedFixture) []string {
		return []string{"channel", "create", "ch" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	}, refused, refused, refused},
	{"channel-delete", func(f gatedFixture) []string { return []string{"channel", "rm", f.channel(""), "--all"} }, refused, refused, refused},
	{"channel-rename", func(f gatedFixture) []string {
		return []string{"channel", "rename", f.channel(""), "n" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	}, refused, refused, refused},
	{"channel-add", func(f gatedFixture) []string { return []string{"channel", "add", f.channel(""), "worker"} }, refused, refused, refused},
	{"channel-remove", func(f gatedFixture) []string { return []string{"channel", "rm", f.channel("worker"), "worker"} }, refused, refused, refused},
	{"queue-cancel", func(f gatedFixture) []string { return []string{"queue", "rm", f.queued()} }, refused, runs, refused},
	{"queue-clear", func(f gatedFixture) []string { return []string{"queue", "clear", "widget-other"} }, refused, runs, refused},
}

func TestEveryGatedCommandAnswersEachCallerAsItsRuleSays(t *testing.T) {
	var covered []string
	for _, c := range gatedCommands {
		covered = append(covered, c.action)
	}
	for _, action := range gatedActions {
		if !slices.Contains(covered, action) {
			t.Errorf("the gated action %s has no row in gatedCommands", action)
		}
	}
	for _, c := range gatedCommands {
		t.Run(c.action, func(t *testing.T) {
			f := newGatedFixture(t)
			for _, caller := range []struct{ session, want string }{{"worker", c.worker}, {"gadget-lead", c.otherLead}, {"widget-lead", c.ownLead}} {
				t.Setenv("AGENTHAIL_SESSION_ID", caller.session)
				args := c.args(f)
				_, err := captureStdout(t, func() error { return f.app.Run(args) })
				switch {
				case caller.want == refused && (err == nil || !strings.Contains(err.Error(), "is for the person") || !strings.Contains(err.Error(), "--ask-person")):
					t.Errorf("%s %v: err=%v, want a refusal that says how to ask the person", caller.session, args, err)
				case caller.want == refused && sendsThePersonToATerminal(err.Error()):
					t.Errorf("%s %v: the refusal %q tells the person to run something", caller.session, args, err)
				case caller.want == runs && err != nil:
					t.Errorf("%s %v: err=%v, want it to run", caller.session, args, err)
				}
			}
		})
	}
}

func TestARefusedAgentAsksThePersonByRunningTheSameCommandAgain(t *testing.T) {
	f := newGatedFixture(t)
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	output, err := captureStdout(t, func() error {
		return f.app.Run([]string{"project", "archive", widgetProject, "--ask-person", "--reason", "the release shipped"})
	})
	if err != nil || !strings.Contains(output, "Asked the person to allow this") || !strings.Contains(output, "arrives as your next message") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	rows, err := f.r.RowAsks()
	if err != nil {
		t.Fatal(err)
	}
	ask, found, err := f.r.Ask(rows["worker"].ID)
	if err != nil || !found || ask.Origin != registry.AskOriginApproval || ask.State != registry.AskOpen || !strings.Contains(ask.Questions[0].Question, "Archive project widget. Why: the release shipped") {
		t.Fatalf("the person's ask is %+v (%v)", ask, err)
	}
	if !strings.Contains(output, ask.ID) {
		t.Fatalf("the output %q does not name the ask %s", output, ask.ID)
	}
	projects, err := f.r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range projects {
		if project.ID == widgetProject && !project.ArchivedAt.IsZero() {
			t.Fatal("asking the person archived the project")
		}
	}
}

func TestAnAgentAsksThePersonToSendOrControlAnotherProjectsSession(t *testing.T) {
	f := newGatedFixture(t)
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	if err := f.app.Run([]string{"interrupt", "gadget-other"}); err == nil || !strings.Contains(err.Error(), "--ask-person") {
		t.Fatalf("interrupting another project's session answered %v, want a refusal that says how to ask", err)
	}
	commands := []struct {
		args     []string
		question string
		body     map[string]any
	}{
		{[]string{"interrupt", "gadget-other"}, "Interrupt gadget-other's turn", map[string]any{"action": "interrupt", "sessionId": "gadget-other"}},
		{[]string{"steer", "gadget-other", "stop the deploy"}, `Steer gadget-other's turn with this message: "stop the deploy"`, map[string]any{"action": "steer", "sessionId": "gadget-other", "message": "stop the deploy", "sourceSessionId": "worker"}},
		{[]string{"send", "gadget-other", "the API moved to v2"}, `Send gadget-other this message: "the API moved to v2"`, map[string]any{"action": "send", "sessionId": "gadget-other", "message": "the API moved to v2", "sourceSessionId": "worker", "busyDelivery": "queue"}},
		{[]string{"thread", "archive", "gadget-other"}, "Archive session gadget-other", map[string]any{"action": "session-archive", "sessionId": "gadget-other"}},
	}
	for _, command := range commands {
		output, err := captureStdout(t, func() error {
			return f.app.Run(append(command.args, "--ask-person", "--reason", "their release depends on it"))
		})
		if err != nil || !strings.Contains(output, "Asked the person to allow this") {
			t.Fatalf("%v answered %q (%v)", command.args, output, err)
		}
		askID := askedID.FindStringSubmatch(output)
		if askID == nil {
			t.Fatalf("%v answered %q, naming no ask", command.args, output)
		}
		found, ok, err := f.r.Approval(askID[1])
		if err != nil || !ok || found.Description != command.question || found.Reason != "their release depends on it" {
			t.Fatalf("%v raised %+v (%v), want %q", command.args, found, err, command.question)
		}
		var stored map[string]any
		if err := json.Unmarshal(found.Payload, &stored); err != nil {
			t.Fatal(err)
		}
		for key, value := range command.body {
			if stored[key] != value {
				t.Fatalf("%v stored %s=%v, want %v", command.args, key, stored[key], value)
			}
		}
	}
	if archived, _ := f.r.SessionArchived("gadget-other"); archived {
		t.Fatal("asking the person archived the session")
	}
}

var askedID = regexp.MustCompile(`allow this \((ask-[0-9a-f]+)\)`)

var personToTerminal = regexp.MustCompile(`(?i)\b(ask|tell|have|get)\s+(the person|the user|them|your user)\s+to\s+(run|type|open)|\b(person|user)\s+(must|should|can|needs to)\s+run\b|\brun\b[^.]{0,80}\bin\s+(a|their|your)\s+terminal\b|\bin\s+(a|their|your)\s+terminal,?\s+run\b|ask your coordinator to have it done`)

func sendsThePersonToATerminal(text string) bool {
	return personToTerminal.MatchString(text)
}

func TestTheSkillNeverTellsAnAgentToSendThePersonToATerminal(t *testing.T) {
	skill, err := os.ReadFile(filepath.Join("..", "..", "skills", "agenthail-operations", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(skill), "\n") {
		if sendsThePersonToATerminal(line) && !strings.Contains(line, "Never tell the person") {
			t.Errorf("SKILL.md:%d tells the person to run something: %s", i+1, line)
		}
	}
	for _, text := range []string{"ask the person to run agenthail project archive", "In your terminal, run agenthail coordinator set", "ask your coordinator to have it done"} {
		if !sendsThePersonToATerminal(text) {
			t.Errorf("the check misses %q", text)
		}
	}
}

func TestAskPersonFlagsLeaveOtherCommandsReasonsAlone(t *testing.T) {
	for _, c := range []struct {
		args   []string
		rest   []string
		ask    bool
		reason string
	}{
		{[]string{"answer", "@w", "deny", "--reason", "not now"}, []string{"answer", "@w", "deny", "--reason", "not now"}, false, ""},
		{[]string{"ledger", "work", "close", "w-1", "--reason=done"}, []string{"ledger", "work", "close", "w-1", "--reason=done"}, false, ""},
		{[]string{"project", "archive", "widget", "--ask-person", "--reason", "shipped"}, []string{"project", "archive", "widget"}, true, "shipped"},
	} {
		rest, ask, reason := askPersonFlags(c.args)
		if !slices.Equal(rest, c.rest) || ask != c.ask || reason != c.reason {
			t.Errorf("%v: rest=%v ask=%v reason=%q", c.args, rest, ask, reason)
		}
	}
}
