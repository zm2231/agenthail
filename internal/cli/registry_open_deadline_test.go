package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestARegistryOpenedDuringATimedCommandIsBoundToItsDeadline(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Stream: true}}
	app, r := cliFixture(t, fake)
	path := r.Path()
	r.Close()
	app.Registry = nil
	app.OpenRegistry = func() (*registry.Registry, error) { return registry.Open(path) }
	holdRegistryWriteLock(t, path)
	started := time.Now()
	err := app.Run([]string{"stream", "codex:s", "--timeout", "300ms"})
	app.Close()
	if elapsed := time.Since(started); elapsed > 1300*time.Millisecond {
		t.Fatalf("opening the registry inside a timed command took %s while it was busy (err %v)", elapsed, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want the command's deadline", err)
	}
}
