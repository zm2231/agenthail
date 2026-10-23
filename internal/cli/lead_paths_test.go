package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLeadPathAddressesTheCoordinatorsSubagents(t *testing.T) {
	const lead, ada, euclid, decoy = "019fb001-0000-7000-8000-000000000001", "019fb002-0000-7000-8000-000000000002", "019fb003-0000-7000-8000-000000000003", "019fb004-0000-7000-8000-000000000004"
	codex := func(id, name string, subagent *surface.Subagent) surface.Session {
		return surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Name: name, Status: surface.StatusIdle, Transport: "managed", Subagent: subagent}
	}
	sessions := map[string]surface.Session{
		lead:   codex(lead, "widget coordinator", nil),
		ada:    codex(ada, "", &surface.Subagent{ParentID: lead, RootID: lead, Depth: 1, Nickname: "Ada"}),
		euclid: codex(euclid, "", &surface.Subagent{ParentID: ada, RootID: lead, Depth: 2, Nickname: "Euclid"}),
		decoy:  codex(decoy, "lead", nil),
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: sessions, caps: surface.Capabilities{Send: true, Steer: true}}
	app, r := cliFixture(t, fake)
	for _, id := range []string{lead, ada, euclid, decoy} {
		if err := r.RegisterSession(sessions[id]); err != nil {
			t.Fatal(err)
		}
	}
	dir := mustEvalSymlinks(t, t.TempDir())
	t.Chdir(dir)
	if err := r.AddProject(registry.ProjectRecord{ID: "project-widget", WorkspaceRoot: dir, Title: "widget", Added: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-widget", ProjectRoot: dir, Name: "widget-lead", Surface: string(surface.KindCodex), Cwd: dir}, registry.CoordinatorChange{SessionID: lead}); err != nil {
		t.Fatal(err)
	}

	for target, want := range map[string]string{"@lead/Ada": ada, "@LEAD/ada/Euclid": euclid} {
		if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", target, "status?"}) }); err != nil {
			t.Fatalf("%s: err=%v", target, err)
		}
		if count := r.QueueCount(want); count != 1 {
			t.Fatalf("%s queued %d messages for %s, want 1", target, count, want)
		}
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead/Ada", "hello Ada"}) }); err != nil || len(fake.sent) != 1 || fake.sent[0] != surface.CLILabel+"hello Ada" {
		t.Fatalf("send err=%v sent=%v", err, fake.sent)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "@lead/Nobody", "lost"}) }); err == nil || !strings.Contains(err.Error(), "Nobody") {
		t.Fatalf("unknown nickname err=%v", err)
	}
	if count := r.QueueCount(decoy) + r.QueueCount(lead); count != 0 {
		t.Fatalf("a lead path reached the session named lead or the coordinator itself: %d", count)
	}

	identity, err := r.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{identity.Name + "::@lead": lead, identity.Name + "::@lead/Ada": ada} {
		before := r.QueueCount(want)
		if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", target, "this host"}) }); err != nil {
			t.Fatalf("%s: err=%v", target, err)
		}
		if r.QueueCount(want) != before+1 {
			t.Fatalf("%s did not queue for %s", target, want)
		}
	}
	if count := r.QueueCount(decoy); count != 0 {
		t.Fatalf("this host's @lead reached the session named lead: %d", count)
	}
}

func TestLeadPathOnAPairedHostIsResolvedThere(t *testing.T) {
	app, reg, fake, api := federatedCLIFixture(t, true)
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/example/widget.git"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
	}
	t.Chdir(repo)
	if err := reg.ReplaceHostCoordinators(vpsHostID, []registry.CoordinatorEntry{{ProjectID: "repo:github.com/example/widget", ProjectRoot: "/home/remote/widget", Name: "widget-lead", Surface: "codex", SessionID: "remote-1"}}); err != nil {
		t.Fatal(err)
	}
	ada := vpsBuilder
	ada.ID, ada.Alias, ada.Name = vpsBuilder.ID+"-ada", "", "Ada"
	api.matches["@remote-1/Ada"] = []remoteTarget{ada}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@lead/Ada", "status?"}) })
	if err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if err := app.Run([]string{"interrupt", "@lead/Ada"}); err != nil {
		t.Fatalf("interrupt err=%v", err)
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "send" || actions[0]["sessionId"] != ada.ID || actions[0]["message"] != "status?" || actions[1]["action"] != "interrupt" || actions[1]["sessionId"] != ada.ID {
		t.Fatalf("actions=%v", actions)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("local surface received %v", fake.sent)
	}
}
