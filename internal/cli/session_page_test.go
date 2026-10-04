package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestActiveLastUsesDaemonPageAndDoesNotReadProvider(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}, result: &surface.SessionReadResult{Source: "provider"}}
	app, _ := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) {
		return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: []surface.Exchange{{User: "q", Assistant: "a"}}, NextBefore: 42}}, nil
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 0 || !strings.Contains(output, `"source":"journal"`) || !strings.Contains(output, `"nextBefore":42`) {
		t.Fatalf("provider requests=%d output=%s", len(provider.requests), output)
	}
}

func TestActiveDaemonFailureDoesNotFallBackToProvider(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}, result: &surface.SessionReadResult{Source: "provider"}}
	app, _ := cliFixture(t, provider.cliSurface)
	app.Surfaces[0].Surface = provider
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return nil, errors.New("daemon unavailable") }

	if err := app.Run([]string{"last", "codex:s"}); err == nil || !strings.Contains(err.Error(), "active daemon session page") {
		t.Fatalf("err=%v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider was read %d times", len(provider.requests))
	}
}

func TestActiveReplyDerivesDoneFromResolvedSessionStatus(t *testing.T) {
	for _, test := range []struct {
		name   string
		status surface.SessionStatus
		done   bool
	}{
		{name: "idle", status: surface.StatusIdle, done: true},
		{name: "busy", status: surface.StatusBusy, done: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := surface.Session{ID: "s", Surface: surface.KindCodex, Status: test.status}
			provider := &cliReadSurface{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}}}
			app, _ := cliFixture(t, provider.cliSurface)
			app.Surfaces[0].Surface = provider
			app.catalogDaemonRunning = func() bool { return true }
			app.daemonSessionPageReader = func() (sessionPageReader, error) {
				return sessionPageFixture{result: &surface.SessionReadResult{Source: "journal", Exchanges: []surface.Exchange{{Assistant: "answer"}}}}, nil
			}
			output, err := captureStdout(t, func() error { return app.Run([]string{"reply", "codex:s", "--json"}) })
			if err != nil || !strings.Contains(output, `"done":`+map[bool]string{true: "true", false: "false"}[test.done]) {
				t.Fatalf("output=%s err=%v", output, err)
			}
		})
	}
}

type sessionPageFixture struct {
	result *surface.SessionReadResult
	err    error
}

func (f sessionPageFixture) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return f.result, f.err
}
