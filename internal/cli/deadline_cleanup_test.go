package cli

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const cleanupTimeout = 200 * time.Millisecond

func lockPastTheDeadline(t *testing.T, app *App) {
	unlock := holdRegistryWriteLock(t, app.Registry.Path())
	go func() {
		time.Sleep(cleanupTimeout + 300*time.Millisecond)
		unlock()
	}()
}

func nameIsFree(t *testing.T, app *App, name string, started time.Time) {
	t.Helper()
	if elapsed := time.Since(started); elapsed > cleanupTimeout+cleanupBudget+time.Second {
		t.Fatalf("the command and its cleanup took %s", elapsed)
	}
	if _, err := app.Registry.ReserveAlias(name, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("a command that ran out of time kept the name @%s: %v", name, err)
	}
}

type deadlineForker struct {
	*cliSurface
	during func()
}

func (f *deadlineForker) ForkSession(ctx context.Context, _ *surface.Session, _ surface.ForkOptions) (*surface.Session, error) {
	f.during()
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestAForkPastItsDeadlineFreesItsNameOnceTheRegistryIsFree(t *testing.T) {
	source := surface.Session{ID: "source", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: "/source"}
	forker := &deadlineForker{cliSurface: &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"source": source}}}
	app, _ := cliFixture(t, forker.cliSurface)
	app.Surfaces[0].Surface = forker
	if err := app.Registry.RegisterSession(source); err != nil {
		t.Fatal(err)
	}
	forker.during = func() { lockPastTheDeadline(t, app) }
	started := time.Now()
	if err := app.Run([]string{"thread", "fork", "codex:source", "--alias", "branch", "--timeout", cleanupTimeout.String()}); err == nil {
		t.Fatal("a fork that ran out of time succeeded")
	}
	nameIsFree(t, app, "branch", started)
}

func TestANamedNotionSendPastItsDeadlineFreesItsName(t *testing.T) {
	synthetic := surface.Session{ID: "new:launch-notes", Surface: surface.KindNotion, RuntimeKind: surface.RuntimeNotionWeb, Name: "launch-notes", Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{"new:launch-notes": synthetic}, caps: surface.Capabilities{Send: true}, sendWait: true}
	app, _ := cliFixture(t, fake)
	started := time.Now()
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"send", "notion:new:launch-notes", "draft", "--timeout", cleanupTimeout.String()})
	}); err == nil {
		t.Fatal("a send that ran out of time succeeded")
	}
	nameIsFree(t, app, "launch-notes", started)
}
