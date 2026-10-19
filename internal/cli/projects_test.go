package cli

import (
	"strings"
	"testing"
)

const projectsSnapshot = `{"projects":[
{"id":"project-widget","displayName":"widget","kind":"git","sessionCount":3,"checkouts":[
 {"host":{"id":"local","name":"mac","state":"local"},"rootPath":"/work/widget","sessionCount":2},
 {"host":{"id":"h0123456789ab","name":"forge","state":"connected"},"rootPath":"/home/remote/widget","sessionCount":1}]},
{"id":"h0123456789ab::project-scratch","displayName":"widget","kind":"git","sessionCount":1,"checkouts":[
 {"host":{"id":"h0123456789ab","name":"forge","state":"connected"},"rootPath":"/home/remote/scratch/widget","sessionCount":1}]},
{"id":"project-notes","displayName":"notes","kind":"folder","archived":true,"checkouts":[
 {"host":{"id":"local","name":"mac","state":"local"},"rootPath":"/work/notes","archived":true}]}]}`

func TestProjectListHidesArchivedUnlessAll(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.snapshot = projectsSnapshot
	output, err := captureStdout(t, func() error { return app.Run([]string{"project", "list"}) })
	if err != nil || !strings.Contains(output, "mac,forge") || !strings.Contains(output, " /work/widget\n") || !strings.Contains(output, " /home/remote/scratch/widget\n") || strings.Contains(output, "notes") {
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
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"project", "archive", "h0123456789ab::project-scratch"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"project", "unarchive", "NOTES"}) }); err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "project-archive" || actions[0]["projectId"] != "h0123456789ab::project-scratch" || actions[1]["action"] != "project-unarchive" || actions[1]["projectId"] != "project-notes" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestProjectAddSendsAnAbsolutePath(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	folder := t.TempDir()
	api.response = `{"ok":true,"project":{"id":"project-x","displayName":"x","rootPath":"` + folder + `","kind":"folder"}}`
	output, err := captureStdout(t, func() error { return app.Run([]string{"project", "add", folder}) })
	if err != nil || !strings.Contains(output, "added project x") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "project-add" || actions[0]["path"] != folder {
		t.Fatalf("actions=%v", actions)
	}
}

func TestThreadArchiveRoutesARemoteSessionThroughTheDaemon(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.response = `{"ok":true}`
	if _, err := captureStdout(t, func() error { return app.Run([]string{"thread", "archive", "forge::@builder"}) }); err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "session-archive" || actions[0]["sessionId"] != forgeBuilder.ID {
		t.Fatalf("actions=%v", actions)
	}
}
