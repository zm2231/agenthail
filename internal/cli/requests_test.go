package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// hostedClaudeApp runs a CLI whose Claude session is open in a running host.
func hostedClaudeApp(t *testing.T) (*App, *registry.Registry, *claudehosttest.Fixture, claudehost.Client, string) {
	t.Helper()
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	client := host.Start(t)
	id := uuid.NewString()
	host.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	app.Surfaces[0].Surface = claude
	var session surface.Session
	claudehosttest.WaitFor(t, "the host session", func() bool {
		sessions, err := claude.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, listed := range sessions {
			if listed.ID == id && listed.RuntimeKind == surface.RuntimeClaudeHost {
				session = listed
				return true
			}
		}
		return false
	})
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("worker", id); err != nil {
		t.Fatal(err)
	}
	return app, r, host, client, id
}

func TestAnswerPicksAQuestionsOptionByNumberAndASecondAnswerIsStale(t *testing.T) {
	app, r, host, client, id := hostedClaudeApp(t)
	if _, err := client.Send(context.Background(), claudehost.SendRequest{SessionID: id, UUID: uuid.NewString(), Text: "QUESTION", Origin: &claudehost.Origin{Kind: "human"}}); err != nil {
		t.Fatal(err)
	}
	var listing string
	claudehosttest.WaitFor(t, "the question", func() bool {
		output, err := captureStdout(t, func() error { return app.Run([]string{"requests", "@worker"}) })
		if err != nil {
			t.Fatal(err)
		}
		listing = output
		return strings.Contains(listing, "Which color?")
	})
	if !strings.Contains(listing, "1. Red") || !strings.Contains(listing, "2. Blue") {
		t.Fatalf("requests = %q", listing)
	}
	if err := app.Run([]string{"answer", "@worker", "allow"}); err == nil {
		t.Fatal("allow answered a question")
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"answer", "@worker", "2"}) })
	if err != nil || !strings.Contains(output, "answered") {
		t.Fatalf("answer = %q, %v", output, err)
	}
	claudehosttest.WaitFor(t, "the answered turn", func() bool {
		turns := host.Turns(t, id)
		return len(turns) == 1 && turns[0].Reply == `question:allow:{"Which color?":"Blue"}`
	})
	if err := app.Run([]string{"answer", "@worker", "2"}); err == nil || !strings.Contains(err.Error(), "no open requests") {
		t.Fatalf("second answer = %v, want no open requests", err)
	}
	entries, err := r.ListHistory(50, id)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		found = found || (entry.Kind == "request-answered" && entry.Result == "answered by the CLI")
	}
	if !found {
		t.Fatalf("history = %+v", entries)
	}
}

func TestPermissionModeShowsAndChangesAHostSessionsMode(t *testing.T) {
	app, r, _, _, id := hostedClaudeApp(t)
	output, err := captureStdout(t, func() error { return app.Run([]string{"permission-mode", "@worker"}) })
	if err != nil || strings.TrimSpace(output) != "default (from Claude settings)" {
		t.Fatalf("permission-mode = %q, %v", output, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"permission-mode", "@worker", "plan"}) }); err != nil {
		t.Fatal(err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"permission-mode", "@worker"}) })
	if err != nil || strings.TrimSpace(output) != "plan" {
		t.Fatalf("permission-mode after the change = %q, %v", output, err)
	}
	entries, err := r.ListHistory(50, id)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, entry := range entries {
		if entry.Kind == "permission-mode" && entry.Message == "plan" && entry.Result == "changed by the CLI" {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("history = %+v", entries)
	}
}

func TestAHostSessionTakesAModelButNoRemoteControlCommands(t *testing.T) {
	app, r, _, _, id := hostedClaudeApp(t)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"model", "@worker", "opus"}) }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"status", "context", "usage"} {
		_, err := captureStdout(t, func() error { return app.Run([]string{"control", "@worker", command}) })
		if err == nil || !strings.Contains(err.Error(), "does not support Claude Remote Control commands") {
			t.Fatalf("control %s = %v, want unsupported", command, err)
		}
	}
	entries, err := r.ListHistory(50, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Kind == "model" && entry.Message == "opus" && entry.Result == "changed by the CLI" {
			return
		}
	}
	t.Fatalf("history = %+v", entries)
}

func TestAnInputRequestIsAnsweredWithItsFieldsOrDeclined(t *testing.T) {
	request := surface.Request{ID: "e1", Kind: surface.WaitingInput, Elicitation: &surface.Elicitation{Server: "deploy", Mode: "form"}}
	decision, err := requestDecision(request, "", `{"env":"prod","replicas":3}`, "")
	if err != nil || decision.Behavior != "allow" || string(decision.Content) != `{"env":"prod","replicas":3}` {
		t.Fatalf("answers = %+v, %v", decision, err)
	}
	if decision, err := requestDecision(request, "allow", "", ""); err != nil || decision.Behavior != "allow" || decision.Content != nil {
		t.Fatalf("allow = %+v, %v", decision, err)
	}
	if decision, err := requestDecision(request, "deny", "", "no"); err != nil || decision.Behavior != "deny" {
		t.Fatalf("deny = %+v, %v", decision, err)
	}
	for _, reply := range []string{"always", "prod", "1"} {
		if _, err := requestDecision(request, reply, "", ""); err == nil {
			t.Fatalf("an input request took reply %q", reply)
		}
	}
	if _, err := requestDecision(request, "", `["prod"]`, ""); err == nil {
		t.Fatal("an input request took answers that are not an object")
	}
}
