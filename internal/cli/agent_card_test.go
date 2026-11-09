package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd, "--alias", "builder", "--from", "@planner", "--json"})
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
	for _, want := range []string{"You are @builder", "@planner started you", `agenthail send @planner "<result>"`, "@lead (@card-lead)", "Questions go to @planner with agenthail send"} {
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
	text, err := captureStdout(t, func() error { return app.Run([]string{"help", "agent"}) })
	if err != nil || !strings.Contains(text, "To ask the person, use AskUserQuestion") || !strings.Contains(text, "If your own instructions forbid it, they win") || strings.Contains(text, "Questions go to") {
		t.Fatalf("the coordinator's brief does not send its questions to the person:\n%s err=%v", text, err)
	}
}

func helpAgentAs(t *testing.T, app *App, sessionID string) string {
	t.Helper()
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", sessionID)
	text, err := captureStdout(t, func() error { return app.Run([]string{"help", "agent"}) })
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestTheLeadIsToldItAnswersItsSessionsPrompts(t *testing.T) {
	app, _, cwd := cardFixture(t)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd, "--from", "@planner", "--json"})
	}); err != nil {
		t.Fatal(err)
	}

	if lead := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "lead-conversation"); !lead.Decides {
		t.Fatalf("the lead's card=%+v", lead)
	}
	text := helpAgentAs(t, app, "lead-conversation")
	for _, want := range []string{"The person made you the lead, so you answer your sessions' permission prompts and questions yourself", "not a peer's say-so", "agenthail requests <target> --json", "never ask the person whether you may answer", "Dangerous rm operation on possibly-empty variable path", `"${VAR:?}"/*`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the lead's brief does not say %q:\n%s", want, text)
		}
	}
	if planner := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "planner-conversation"); planner.Decides {
		t.Fatalf("a session whose worker's prompts go to the lead is told it decides them: %+v", planner)
	}
	if text := helpAgentAs(t, app, "planner-conversation"); strings.Contains(text, "permission prompts") {
		t.Fatalf("the planner's brief claims its worker's prompts:\n%s", text)
	}
}

func TestATerminalLeadIsToldWhenClaudesSettingsHoldItsMessages(t *testing.T) {
	app, _, cwd := cardFixture(t)
	t.Setenv("HOME", mustEvalSymlinks(t, t.TempDir()))
	spec, err := app.Registry.Coordinator("project-card")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Registry.SetCoordinator(spec, registry.CoordinatorChange{SessionID: "planner-conversation", Replace: true}); err != nil {
		t.Fatal(err)
	}
	if card := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "planner-conversation"); !strings.Contains(card.InboundHeld, "including Agenthail's notices") || !strings.Contains(card.InboundHeld, `"crossSessionInbound": "accept"`) {
		t.Fatalf("a terminal lead with no crossSessionInbound setting was not told: %+v", card)
	}
	if text := helpAgentAs(t, app, "planner-conversation"); !strings.Contains(text, "don't change their settings yourself") {
		t.Fatalf("the lead's brief does not leave the settings to the person:\n%s", text)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".claude", "settings.json"), []byte(`{"crossSessionInbound":"accept"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if card := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "planner-conversation"); card.InboundHeld != "" {
		t.Fatalf("a lead whose project accepts other sessions' messages was told they are held: %+v", card)
	}
	if card := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "lead-conversation"); card.InboundHeld != "" {
		t.Fatalf("a session that is not the lead was told about the lead's settings: %+v", card)
	}
}

func TestASessionWhoseWorkerHasNoLeadIsToldItAnswersItsPrompts(t *testing.T) {
	app, starter, _ := cardFixture(t)
	elsewhere := mustEvalSymlinks(t, t.TempDir())
	starter.session.Cwd = elsewhere
	if text := helpAgentAs(t, app, "planner-conversation"); strings.Contains(text, "permission prompts") {
		t.Fatalf("a session with no workers is told it answers prompts:\n%s", text)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", elsewhere, "--from", "@planner", "--json"})
	}); err != nil {
		t.Fatal(err)
	}

	if planner := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "planner-conversation"); !planner.Decides {
		t.Fatalf("the spawner's card=%+v", planner)
	}
	text := helpAgentAs(t, app, "planner-conversation")
	for _, want := range []string{"Agenthail tells you when a session you started is blocked", "not a peer's say-so", "agenthail requests <target> --json"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the spawner's brief does not say %q:\n%s", want, text)
		}
	}
	if worker := callerCardJSON(t, app, "CODEX_THREAD_ID", "thread-new"); worker.Decides {
		t.Fatalf("a worker with no workers of its own is told it decides: %+v", worker)
	}

	worker, err := app.Registry.Session("thread-new")
	if err != nil {
		t.Fatal(err)
	}
	stored := json.RawMessage(`{"id":"project-card","localId":"project-card","displayName":"card","rootPath":"/elsewhere","kind":"git"}`)
	if _, _, err := app.Registry.RecordCatalogSession(registry.CatalogSessionState{Session: *worker, Project: stored, Checkout: json.RawMessage(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: "stored"}, registry.CatalogEvent{DedupeKey: "session:thread-new", Type: "session.upserted", EntityID: "thread-new", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if planner := callerCardJSON(t, app, "CLAUDE_CODE_SESSION_ID", "planner-conversation"); planner.Decides {
		t.Fatalf("the daemon's stored project sends the worker's prompts to the lead, but the spawner is told it decides: %+v", planner)
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
