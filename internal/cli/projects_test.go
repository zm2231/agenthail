package cli

import (
	"strings"
	"testing"
)

const projectsSnapshot = `{"projects":[
{"id":"project-widget","displayName":"widget","rootPath":"/work/widget","kind":"git","sessionCount":2,"coordinator":{"name":"widget-lead","surface":"claude","sessionId":"s1"}},
{"id":"h0123456789ab::project-widget","displayName":"widget","rootPath":"/home/remote/widget","kind":"git","host":{"id":"h0123456789ab","name":"forge"},"sessionCount":1},
{"id":"project-notes","displayName":"notes","rootPath":"/work/notes","kind":"folder","archived":true}]}`

func TestProjectListHidesArchivedUnlessAll(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.snapshot = projectsSnapshot
	output, err := captureStdout(t, func() error { return app.Run([]string{"project", "list"}) })
	if err != nil || !strings.Contains(output, "/work/widget") || !strings.Contains(output, "forge") || !strings.Contains(output, "@widget-lead") || strings.Contains(output, "notes") {
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
		return app.Run([]string{"project", "archive", "h0123456789ab::project-widget"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"project", "unarchive", "NOTES"}) }); err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "project-archive" || actions[0]["projectId"] != "h0123456789ab::project-widget" || actions[1]["action"] != "project-unarchive" || actions[1]["projectId"] != "project-notes" {
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
