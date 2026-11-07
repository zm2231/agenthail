package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func timedStreamOpensRegistry(t *testing.T, open func(context.Context) (*registry.Registry, error), lockPath string) {
	t.Helper()
	session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Stream: true}}
	app, r := cliFixture(t, fake)
	r.Close()
	app.Registry = nil
	app.OpenRegistry = open
	holdRegistryWriteLock(t, lockPath)
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

func TestARegistryOpenedDuringATimedCommandIsBoundToItsDeadline(t *testing.T) {
	path := registrytestPath(t)
	timedStreamOpensRegistry(t, func(context.Context) (*registry.Registry, error) { return registry.Open(path) }, path)
}

func TestATimedCommandThatMustMigrateTheRegistryEndsAtItsDeadline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r, err := registry.OpenDefault(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	path := r.Path()
	r.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", registry.SchemaVersion()-1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	timedStreamOpensRegistry(t, func(ctx context.Context) (*registry.Registry, error) { return registry.OpenDefault(ctx, true) }, path)
}

func registrytestPath(t *testing.T) string {
	t.Helper()
	r, err := registry.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatal(err)
	}
	path := r.Path()
	r.Close()
	return path
}
