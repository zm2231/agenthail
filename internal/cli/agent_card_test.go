package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agentcard"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func cardFixture(t *testing.T) (*App, *starterCLISurface, string) {
	t.Helper()
	cwd := mustEvalSymlinks(t, t.TempDir())
	starter := &starterCLISurface{
		cliSurface: &cliSurface{kind: surface.KindCodex},
		session:    &surface.Session{ID: "thread-new", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: cwd, Status: surface.StatusBusy, Source: "agenthail"},
		delivery:   &surface.SendResult{UUID: "turn-1", Accepted: true},
	}
	app := threadFixture(t, starter)
	store := app.Registry
	if err := store.AddProject(registry.ProjectRecord{ID: "project-card", WorkspaceRoot: cwd, Title: "card", Added: true}); err != nil {
		t.Fatal(err)
	}
	for _, session := range []surface.Session{
		{ID: "planner-conversation", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: cwd},
		{ID: "lead-conversation", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Cwd: cwd},
	} {
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetAlias("planner", "planner-conversation"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-card", ProjectRoot: cwd, Name: "card-lead", Surface: string(surface.KindClaude), Cwd: cwd}, registry.CoordinatorChange{SessionID: "lead-conversation"}); err != nil {
		t.Fatal(err)
	}
	return app, starter, cwd
}

func callerCardJSON(t *testing.T, app *App, variable, id string) agentcard.Card {
	t.Helper()
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	t.Setenv(variable, id)
	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami", "--json"}) })
	var card agentcard.Card
	if err != nil || json.Unmarshal([]byte(output), &card) != nil {
		t.Fatalf("whoami output=%q err=%v", output, err)
	}
	return card
}

func TestASessionAnAgentStartsReportsToThatAgent(t *testing.T) {
	app, _, cwd := cardFixture(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "planner-conversation")
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd, "--alias", "builder", "--json"})
	}); err != nil {
		t.Fatal(err)
	}

	card := callerCardJSON(t, app, "CODEX_THREAD_ID", "thread-new")
	if !card.Resolved || card.Alias != "builder" || card.Project == nil || card.Project.Name != "card" {
		t.Fatalf("card=%+v", card)
	}
	if card.StartedBy == nil || card.StartedBy.Via != registry.OriginAgent || card.StartedBy.Ref != "@planner" || card.ReportTo != "@planner" {
		t.Fatalf("startedBy=%+v reportTo=%q", card.StartedBy, card.ReportTo)
	}
	if card.Lead == nil || card.Lead.Ref != "@card-lead" || card.Lead.Self {
		t.Fatalf("lead=%+v", card.Lead)
	}

	text, err := captureStdout(t, func() error { return app.Run([]string{"help", "agent"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You are @builder", "@planner started you", `agenthail send @planner "<result>"`, "@lead (@card-lead)", "request_user_input"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help agent does not say %q:\n%s", want, text)
		}
	}
}

func TestASessionThePersonStartsReportsToTheCoordinator(t *testing.T) {
	app, _, cwd := cardFixture(t)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd, "--json"})
	}); err != nil {
		t.Fatal(err)
	}

	card := callerCardJSON(t, app, "CODEX_THREAD_ID", "thread-new")
	if card.StartedBy == nil || card.StartedBy.Via != registry.OriginPerson || card.ReportTo != "@lead" || card.Alias != "" {
		t.Fatalf("card=%+v", card)
	}
	lead := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "lead-conversation")
	if lead.Lead == nil || !lead.Lead.Self || lead.ReportTo != "" || lead.Ask == nil || !lead.Ask.Answerable {
		t.Fatalf("the coordinator's own card=%+v", lead)
	}
}

func TestThreadCreateRefusesACallerFromAnotherAgenthail(t *testing.T) {
	app, starter, cwd := cardFixture(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "conversation-of-another-registry")
	err := app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd})
	if err == nil || !strings.Contains(err.Error(), "--from") || len(starter.options) != 0 {
		t.Fatalf("err=%v starts=%d", err, len(starter.options))
	}
}

func TestThreadCreateFromNamesTheCreator(t *testing.T) {
	app, starter, cwd := cardFixture(t)
	if err := app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd, "--from", "@nobody"}); err == nil || len(starter.options) != 0 {
		t.Fatalf("an unknown --from started a thread: err=%v", err)
	}
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd, "--from", "@planner", "--json"})
	}); err != nil {
		t.Fatal(err)
	}
	card := callerCardJSON(t, app, "CODEX_THREAD_ID", "thread-new")
	if card.StartedBy == nil || card.StartedBy.Ref != "@planner" || len(starter.options) != 1 || starter.options[0].Message != "Build it" {
		t.Fatalf("startedBy=%+v options=%+v", card.StartedBy, starter.options)
	}
}
