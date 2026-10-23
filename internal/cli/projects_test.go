package cli

import (
	"net/http"
	"strings"
	"testing"
)

const projectsSnapshot = `{"projects":[
{"id":"repo:github.com/example/widget","displayName":"widget","kind":"git","key":"repo:github.com/example/widget","sessionCount":3,"checkouts":[
 {"host":{"id":"local","name":"laptop","state":"local"},"projectId":"p-widget-laptop","rootPath":"/work/widget","added":true,"sessionCount":2},
 {"host":{"id":"h0123456789ab","name":"vps","state":"connected"},"projectId":"h0123456789ab::implicit:/home/remote/widget","rootPath":"/home/remote/widget","sessionCount":1}]},
{"id":"h0123456789ab::implicit:/home/remote/scratch/widget","displayName":"widget","kind":"git","sessionCount":1,"checkouts":[
 {"host":{"id":"h0123456789ab","name":"vps","state":"connected"},"projectId":"h0123456789ab::implicit:/home/remote/scratch/widget","rootPath":"/home/remote/scratch/widget","sessionCount":1}]},
{"id":"p-notes","displayName":"notes","kind":"folder","archived":true,"checkouts":[
 {"host":{"id":"local","name":"laptop","state":"local"},"projectId":"p-notes","rootPath":"/work/notes","added":true,"archived":true}]}]}`

func TestProjectListHidesArchivedUnlessAll(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.snapshot = projectsSnapshot
	output, err := captureStdout(t, func() error { return app.Run([]string{"project", "list"}) })
	if err != nil || !strings.Contains(output, "laptop,vps") || !strings.Contains(output, " /work/widget\n") || !strings.Contains(output, " /home/remote/scratch/widget\n") || strings.Contains(output, "notes") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"project", "list", "--all"}) })
	if err != nil || !strings.Contains(output, "notes (archived)") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestProjectArchiveResolvesByIdAndRejectsAmbiguousNames(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.snapshot = projectsSnapshot
	api.response = `{"ok":true}`
	if err := app.Run([]string{"project", "archive", "widget"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err=%v", err)
	}
	if err := app.Run([]string{"project", "archive", "repo:github.com/example/widget-before"}); err == nil || err.Error() != `no project matched "repo:github.com/example/widget-before"; a project's id can change, for example after its repository's origin changed; see 'agenthail project list --all' for the current ones` {
		t.Fatalf("err=%v", err)
	}
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"project", "archive", "h0123456789ab::implicit:/home/remote/scratch/widget"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"project", "unarchive", "NOTES"}) }); err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "project-archive" || actions[0]["projectId"] != "h0123456789ab::implicit:/home/remote/scratch/widget" || actions[1]["action"] != "project-unarchive" || actions[1]["projectId"] != "p-notes" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestProjectAddSendsAnAbsolutePathAndTitle(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	folder := t.TempDir()
	api.response = `{"ok":true,"project":{"id":"p-x","localId":"p-x","displayName":"Web app","rootPath":"` + folder + `","kind":"folder","added":true},"created":true}`
	output, err := captureStdout(t, func() error { return app.Run([]string{"project", "add", folder, "--name", "Web app"}) })
	if err != nil || !strings.Contains(output, "added project Web app") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "project-add" || actions[0]["path"] != folder || actions[0]["title"] != "Web app" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestProjectRemoveSendsTheAddedCheckout(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.snapshot = projectsSnapshot
	api.response = `{"ok":true}`
	if _, err := captureStdout(t, func() error { return app.Run([]string{"project", "remove", "/work/widget"}) }); err != nil {
		t.Fatal(err)
	}
	if err := app.Run([]string{"project", "remove", "h0123456789ab::implicit:/home/remote/scratch/widget"}); err == nil || !strings.Contains(err.Error(), "was not added") {
		t.Fatalf("err=%v", err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "project-remove" || actions[0]["projectId"] != "p-widget-laptop" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestThreadArchiveRoutesARemoteSessionThroughTheDaemon(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.response = `{"ok":true}`
	if _, err := captureStdout(t, func() error { return app.Run([]string{"thread", "archive", "vps::@builder"}) }); err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "session-archive" || actions[0]["sessionId"] != vpsBuilder.ID {
		t.Fatalf("actions=%v", actions)
	}
}

func TestProjectListNamesEachProjectsLeadAsThisHostAddressesIt(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	laptop := `{"id":"local","name":"laptop","state":"local"}`
	vps := `{"id":"h0123456789ab","name":"vps","state":"connected"}`
	api.snapshot = `{"projects":[
{"id":"repo:github.com/example/widget","displayName":"widget","kind":"git","checkouts":[{"host":` + laptop + `,"projectId":"p-widget","rootPath":"/work/widget"}],"coordinators":[{"host":` + laptop + `,"name":"widget-lead","surface":"claude"}]},
{"id":"repo:github.com/example/api","displayName":"api","kind":"git","checkouts":[{"host":` + vps + `,"projectId":"h0123456789ab::implicit:/srv/api","rootPath":"/srv/api"}],"coordinators":[{"host":` + vps + `,"name":"api-lead","surface":"codex"}]},
{"id":"repo:github.com/example/site","displayName":"site","kind":"git","checkouts":[{"host":` + laptop + `,"projectId":"p-site","rootPath":"/work/site"}],"coordinators":[{"host":` + laptop + `,"name":"site-lead","surface":"claude"},{"host":` + vps + `,"name":"site-vps","surface":"codex"}]},
{"id":"p-notes","displayName":"notes","kind":"folder","checkouts":[{"host":` + laptop + `,"projectId":"p-notes","rootPath":"/work/notes"}],"coordinators":[]}]}`
	output, err := captureStdout(t, func() error { return app.Run([]string{"project", "list"}) })
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		if fields := strings.Fields(line); len(fields) > 2 {
			lines[fields[0]] = fields[2]
		}
	}
	if lines["PROJECT"] != "LEAD" || lines["widget"] != "@widget-lead" || lines["api"] != "vps::@api-lead" || lines["notes"] != "-" || !strings.Contains(output, "conflict (2)") {
		t.Fatalf("output=%q", output)
	}
}

func TestProjectRenameNamesTheProjectOnEveryHost(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.snapshot = projectsSnapshot
	api.response = `{"ok":true}`
	output, err := captureStdout(t, func() error { return app.Run([]string{"project", "rename", "/work/widget", "Widget app"}) })
	if err != nil || !strings.Contains(output, "renamed project widget to Widget app") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "project-rename" || actions[0]["projectId"] != "repo:github.com/example/widget" || actions[0]["title"] != "Widget app" {
		t.Fatalf("actions=%v", actions)
	}
	if err := app.Run([]string{"project", "rename", "widget"}); err == nil || !strings.Contains(err.Error(), "project rename") {
		t.Fatalf("err=%v", err)
	}
}

func TestProjectRefusalsCarryTheFlagThatResolvesThem(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.responses = map[string]string{"coordinator-set": `{"error":{"code":"coordinator_exists","message":"the project already has coordinator @widget-lead; replace it to take over"}}`}
	api.statuses = map[string]int{"coordinator-set": http.StatusConflict}
	err := app.Run([]string{"coordinator", "set", "vps::@builder"})
	if err == nil || err.Error() != "the project already has coordinator @widget-lead; replace it to take over (rerun with --replace)" {
		t.Fatalf("err=%v", err)
	}
}

func TestHelpListsThreadCreateCwd(t *testing.T) {
	app, _, _, _ := federatedCLIFixture(t, true)
	output, err := captureStdout(t, func() error { return app.Run([]string{"help"}) })
	if err != nil || !strings.Contains(output, `thread create <codex|claude> "msg" [--cwd <folder>]`) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}
