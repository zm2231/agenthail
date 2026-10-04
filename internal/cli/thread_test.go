package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type starterCLISurface struct {
	*cliSurface
	options  []surface.SessionStartOptions
	session  *surface.Session
	delivery *surface.SendResult
	err      error
	wait     bool
}

func (f *starterCLISurface) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	f.options = append(f.options, options)
	if f.wait {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	return f.session, f.delivery, f.err
}

func threadFixture(t *testing.T, starter *starterCLISurface) *App {
	t.Helper()
	app, _ := cliFixture(t, starter.cliSurface)
	app.Surfaces[0].Surface = starter
	return app
}

func TestThreadCreateCodexRegistersAndPrintsJSON(t *testing.T) {
	cwd := t.TempDir()
	base := &cliSurface{kind: surface.KindCodex}
	starter := &starterCLISurface{
		cliSurface: base,
		session:    &surface.Session{ID: "thread-123", Surface: surface.KindCodex, Name: "Build this", Cwd: cwd, Status: surface.StatusBusy, Source: "agenthail", Transport: "managed"},
		delivery:   &surface.SendResult{UUID: "turn-456", Accepted: true},
	}
	app := threadFixture(t, starter)
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build this", "--cwd", cwd, "--alias", "builder", "--model", "gpt-test", "--approval", "on-request", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(starter.options) != 1 {
		t.Fatalf("options=%+v", starter.options)
	}
	options := starter.options[0]
	if options.Message != "Build this" || options.Cwd != cwd || options.Model != "gpt-test" || options.ApprovalPolicy != "on-request" {
		t.Fatalf("options=%+v", options)
	}
	var result threadCreateOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode output %q: %v", output, err)
	}
	if !result.OK || result.Session == nil || result.Session.ID != "thread-123" || result.Delivery == nil || result.Delivery.UUID != "turn-456" || result.Alias != "builder" {
		t.Fatalf("result=%+v", result)
	}
	registered, err := app.Registry.Session("thread-123")
	if err != nil || registered.Transport != "managed" {
		t.Fatalf("registered=%+v err=%v", registered, err)
	}
	alias, err := app.Registry.LookupAlias("builder")
	if err != nil || alias != "thread-123" {
		t.Fatalf("alias=%q err=%v", alias, err)
	}
	history, err := app.Registry.ListHistory(5, "thread-123")
	if err != nil || len(history) != 1 || history[0].Kind != "sent" || history[0].Result != "turn-456" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestThreadCreateCodexAcceptsMessageFlagAndDefaultsToCallerCwd(t *testing.T) {
	base := &cliSurface{kind: surface.KindCodex}
	starter := &starterCLISurface{
		cliSurface: base,
		session:    &surface.Session{ID: "thread", Surface: surface.KindCodex, Cwd: "/tmp", Transport: "managed"},
		delivery:   &surface.SendResult{UUID: "turn", Accepted: true},
	}
	app := threadFixture(t, starter)
	wantCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "--message", "Use the explicit flag"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(starter.options) != 1 || starter.options[0].Message != "Use the explicit flag" || starter.options[0].Cwd != wantCwd {
		t.Fatalf("options=%+v want cwd=%q", starter.options, wantCwd)
	}
	if !strings.Contains(output, "created codex/thread") || !strings.Contains(output, "started turn turn") {
		t.Fatalf("output=%q", output)
	}
}

func TestThreadCreateCodexReadsLongMessageFromStdin(t *testing.T) {
	prompt := strings.Repeat("x", 100_000)
	path := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.WriteFile(path, []byte(prompt), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	oldStdin := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = oldStdin }()

	base := &cliSurface{kind: surface.KindCodex}
	starter := &starterCLISurface{
		cliSurface: base,
		session:    &surface.Session{ID: "thread", Surface: surface.KindCodex, Cwd: t.TempDir(), Transport: "managed"},
		delivery:   &surface.SendResult{UUID: "turn", Accepted: true},
	}
	app := threadFixture(t, starter)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "--message", "-"})
	}); err != nil {
		t.Fatal(err)
	}
	if len(starter.options) != 1 || starter.options[0].Message != prompt {
		t.Fatalf("message length=%d", len(starter.options[0].Message))
	}
}

func TestThreadCreateCodexPreservesCreatedThreadOnUnknownTurn(t *testing.T) {
	cwd := t.TempDir()
	base := &cliSurface{kind: surface.KindCodex}
	starter := &starterCLISurface{
		cliSurface: base,
		session:    &surface.Session{ID: "created", Surface: surface.KindCodex, Cwd: cwd, Transport: "managed"},
		err:        surface.DeliveryOutcomeUnknown(errors.New("connection closed")),
	}
	app := threadFixture(t, starter)
	output, runErr := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build this", "--cwd", cwd, "--alias", "builder", "--json"})
	})
	if runErr != nil {
		t.Fatalf("err=%v", runErr)
	}
	var result threadCreateOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil || !result.OK || result.Unknown || result.Status != "submitted" || !result.Accepted || result.Retryable || result.DeliveryID <= 0 || result.Session == nil || result.Session.ID != "created" || result.Detail != "Submitted to @builder." || result.Warning != "" || strings.Contains(strings.ToLower(output), "unresolved") || strings.Contains(strings.ToLower(output), "confirm") || strings.Contains(strings.ToLower(output), "inspect") || strings.Contains(strings.ToLower(output), "retrying") {
		t.Fatalf("result=%+v decodeErr=%v output=%q", result, err, output)
	}
	if _, err := app.Registry.Session("created"); err != nil {
		t.Fatalf("created session was not registered: %v", err)
	}
	if alias, err := app.Registry.LookupAlias("builder"); err != nil || alias != "created" {
		t.Fatalf("alias=%q err=%v", alias, err)
	}
	history, err := app.Registry.ListHistory(5, "created")
	if err != nil || len(history) != 1 || history[0].Kind != "submitted" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	intent, err := app.Registry.DeliveryIntent(result.DeliveryID)
	if err != nil || intent.TargetSessionID != "created" || intent.Status != "submitted" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestThreadCreateCodexReportsDefinitiveInitialFailure(t *testing.T) {
	cwd := t.TempDir()
	base := &cliSurface{kind: surface.KindCodex}
	starter := &starterCLISurface{
		cliSurface: base,
		session:    &surface.Session{ID: "created", Surface: surface.KindCodex, Cwd: cwd, Transport: "managed"},
		err:        errors.New("initial message rejected"),
	}
	app := threadFixture(t, starter)
	output, runErr := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build this", "--cwd", cwd, "--json"})
	})
	if runErr == nil {
		t.Fatal("expected definitive creation failure")
	}
	var result threadCreateOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.OK || result.Unknown || result.Status != "failed" || result.Accepted || result.Retryable || result.DeliveryID <= 0 || result.Session == nil {
		t.Fatalf("result=%+v decodeErr=%v output=%q", result, err, output)
	}
	intent, err := app.Registry.DeliveryIntent(result.DeliveryID)
	if err != nil || intent.Status != "failed" || intent.TargetSessionID != "created" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	if got := app.Registry.QueueCount(registry.OperatorSessionID); got != 1 {
		t.Fatalf("failure notice queue count=%d, want 1", got)
	}
	events, err := app.Registry.CatalogEventsAfter(0, 20)
	if err != nil || len(events.Events) == 0 {
		t.Fatalf("catalog events=%+v err=%v", events, err)
	}
	queueEvent := false
	for _, event := range events.Events {
		if event.Type == "session.queue" && event.EntityID == registry.OperatorSessionID {
			queueEvent = true
		}
	}
	if !queueEvent {
		t.Fatalf("catalog events missing operator notice: %+v", events.Events)
	}
	type problemPayload struct {
		DeliveryID      int64  `json:"deliveryId"`
		SessionID       string `json:"sessionId"`
		SourceSessionID string `json:"sourceSessionId"`
		Reason          string `json:"reason"`
	}
	var problem problemPayload
	for _, event := range events.Events {
		if event.Type == "delivery.problem" {
			if err := json.Unmarshal(event.Payload, &problem); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if problem.DeliveryID != result.DeliveryID || problem.SessionID != "created" || problem.SourceSessionID != registry.OperatorSessionID || problem.Reason != "initial message rejected" {
		t.Fatalf("problem payload=%+v", problem)
	}
}

func TestThreadCreateCodexHumanSubmittedDetailIsNeutral(t *testing.T) {
	cwd := t.TempDir()
	starter := &starterCLISurface{
		cliSurface: &cliSurface{kind: surface.KindCodex},
		session:    &surface.Session{ID: "created", Surface: surface.KindCodex, Cwd: cwd, Transport: "managed"},
		err:        surface.DeliveryOutcomeUnknown(errors.New("provider response lost")),
	}
	app := threadFixture(t, starter)
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build this", "--cwd", cwd, "--alias", "builder"})
	})
	if err != nil || output != "Submitted to @builder.\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestThreadCreateCodexValidatesInputs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"missing action", []string{"thread"}, "usage:"},
		{"unsupported surface", []string{"thread", "create", "notion", "hello"}, "requires Codex or Claude"},
		{"missing message", []string{"thread", "create", "codex"}, "message is required"},
		{"two messages", []string{"thread", "create", "codex", "positional", "--message", "flag"}, "either positionally"},
		{"bad approval", []string{"thread", "create", "codex", "hello", "--approval", "always"}, "approval must"},
		{"file cwd", []string{"thread", "create", "codex", "hello", "--cwd", file}, "not a directory"},
		{"unknown flag", []string{"thread", "create", "codex", "hello", "--bogus"}, "unknown flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (&App{}).Run(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestThreadCreateHelpIsSuccessful(t *testing.T) {
	output, err := captureStdout(t, func() error { return (&App{}).Run([]string{"thread", "create", "codex", "--help"}) })
	if err != nil || !strings.Contains(output, "agenthail thread create <codex|claude>") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestThreadCreateCodexHonorsTimeout(t *testing.T) {
	base := &cliSurface{kind: surface.KindCodex}
	starter := &starterCLISurface{cliSurface: base, wait: true}
	app := threadFixture(t, starter)
	started := time.Now()
	err := app.Run([]string{"thread", "create", "codex", "hello", "--timeout", "10ms"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("timeout took %s", time.Since(started))
	}
}
