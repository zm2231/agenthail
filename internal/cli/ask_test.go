package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestASessionRaisesAnAskWithTheCommandAndThePersonAnswersIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app, r, host, _, id := hostedClaudeApp(t)
	startTestDaemon(t, app, r, host.Home)
	t.Setenv("AGENTHAIL_SESSION_ID", id)
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"ask", "Deploy the fix?", "--option", "Deploy", "--option", "Wait: hold until Monday", "--recommend", "2",
			"--question", "Which region first?", "--option", "us", "--option", "eu", "--link", "Runbook=https://example.com/runbook"})
	})
	if err != nil || !strings.Contains(output, "goes to person") {
		t.Fatalf("ask = %q, %v", output, err)
	}
	open, err := r.OpenAsks()
	if err != nil || len(open) != 1 {
		t.Fatalf("open asks %+v err %v", open, err)
	}
	ask := open[0]
	deploy, region := ask.Questions[0], ask.Questions[1]
	if ask.SessionID != id || deploy.Question != "Deploy the fix?" || deploy.Options[1].Does != "hold until Monday" || deploy.Recommended != "o1" ||
		region.Question != "Which region first?" || len(region.Options) != 2 || len(region.Links) != 1 || region.Links[0].URL != "https://example.com/runbook" {
		t.Fatalf("ask %+v", ask)
	}
	for name, args := range map[string][]string{
		"a recommendation before its option": {"ask", "Ship?", "--recommend", "1", "--option", "Yes"},
		"a link without a url":               {"ask", "Ship?", "--option", "Yes", "--link", "Runbook"},
		"no options":                         {"ask", "Ship?"},
	} {
		if err := app.Run(args); err == nil {
			t.Errorf("%s was raised", name)
		}
	}

	t.Setenv("AGENTHAIL_SESSION_ID", "")
	listed, err := captureStdout(t, func() error { return app.Run([]string{"ask", "list"}) })
	if err != nil || !strings.Contains(listed, ask.ID) || !strings.Contains(listed, "Wait (hold until Monday) [recommended]") {
		t.Fatalf("list = %q, %v", listed, err)
	}
	if err := app.Run([]string{"ask", "withdraw", ask.ID}); err == nil {
		t.Fatal("the person withdrew a session's ask")
	}
	if output, err := captureStdout(t, func() error { return app.Run([]string{"ask", "answer", ask.ID, "Wait, then eu"}) }); err != nil || !strings.Contains(output, "answered") {
		t.Fatalf("answer = %q, %v", output, err)
	}
	testkit.Eventually(t, "the answer delivered to the session", 15*time.Second, func() bool {
		turns := host.Turns(t, id)
		return len(turns) == 1 && strings.Contains(turns[0].Text, "Wait, then eu")
	})
	if answered, _, err := r.Ask(ask.ID); err != nil || answered.State != registry.AskAnswered || answered.AnsweredBy != registry.AskToPerson {
		t.Fatalf("ask after the answer %+v err %v", answered, err)
	}

	t.Setenv("AGENTHAIL_SESSION_ID", id)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"ask", "Roll back?", "--option", "Roll back", "--option", "Keep it"})
	}); err != nil {
		t.Fatal(err)
	}
	open, err = r.OpenAsks()
	if err != nil || len(open) != 1 {
		t.Fatalf("open asks %+v err %v", open, err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"ask", "answer", open[0].ID, "o1"}) }); err != nil {
		t.Fatalf("answer by option id: %v", err)
	}
	testkit.Eventually(t, "the picked option delivered to the session", 15*time.Second, func() bool {
		turns := host.Turns(t, id)
		return len(turns) == 2 && strings.Contains(turns[1].Text, "Roll back? Keep it")
	})
}

func TestAnAskWithItemIsOnTheWorkItemItNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app, r, host, _, id := hostedClaudeApp(t)
	if _, err := r.AddProject(registry.ProjectRecord{ID: "p-asks", WorkspaceRoot: host.Cwd, Title: "asks", Added: true}, ""); err != nil {
		t.Fatal(err)
	}
	t.Chdir(host.Cwd)
	startTestDaemon(t, app, r, host.Home)
	t.Setenv("AGENTHAIL_SESSION_ID", id)
	var held []string
	for _, text := range []string{"Import the March export", "Rename the export columns"} {
		added, err := captureStdout(t, func() error { return app.Run([]string{"ledger", "work", "add", text, "--json"}) })
		var result struct {
			Item struct{ ID string } `json:"item"`
		}
		if err != nil || json.Unmarshal([]byte(added), &result) != nil || result.Item.ID == "" {
			t.Fatalf("add = %q, %v", added, err)
		}
		if err := app.Run([]string{"ledger", "work", "claim", result.Item.ID}); err != nil {
			t.Fatal(err)
		}
		held = append(held, result.Item.ID)
	}

	output, err := captureStdout(t, func() error {
		return app.Run([]string{"ask", "Rename to snake_case?", "--option", "Yes", "--option", "No", "--item", held[1]})
	})
	if err != nil || !strings.Contains(output, "is open on "+held[1]) {
		t.Fatalf("ask --item = %q, %v", output, err)
	}
	open, err := r.OpenAsks()
	if err != nil || len(open) != 1 || open[0].WorkItem != held[1] || !open[0].Deadline.IsZero() {
		t.Fatalf("open asks %+v err %v", open, err)
	}
	output, err = captureStdout(t, func() error {
		return app.Run([]string{"ask", "Keep the old names?", "--option", "Rename", "--option", "Keep", "--item", held[0], "--default", "2", "--default-after", "30"})
	})
	if err != nil || !strings.Contains(output, "it takes its default: Keep.") {
		t.Fatalf("ask --default = %q, %v", output, err)
	}
	if err := app.Run([]string{"ask", "Keep?", "--option", "Rename", "--option", "Keep", "--default", "2"}); err == nil || !strings.Contains(err.Error(), "--default-after") {
		t.Fatalf("a default without its minutes: %v", err)
	}
	if err := app.Run([]string{"ask", "Drop it?", "--option", "Yes", "--option", "No", "--item", "nowhere-W9"}); err == nil || !strings.Contains(err.Error(), "not held by this session") {
		t.Fatalf("an ask on an item the session does not hold: %v", err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	listed, err := captureStdout(t, func() error { return app.Run([]string{"ask", "list"}) })
	if err != nil || !strings.Contains(listed, " on "+held[1]+" to person: Rename to snake_case?") {
		t.Fatalf("list = %q, %v", listed, err)
	}
}
