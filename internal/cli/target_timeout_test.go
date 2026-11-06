package cli

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"

	_ "modernc.org/sqlite"
)

func holdRegistryWriteLock(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		conn.Close()
		db.Close()
	})
}

func TestEveryTimedCommandEndsAtItsTimeoutWhileTheRegistryIsBusy(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, args := range [][]string{
		{"stream", "codex:s"},
		{"send", "codex:s", "hello"},
		{"reply", "codex:s"},
		{"last", "codex:s"},
		{"interrupt", "codex:s"},
		{"steer", "codex:s", "hello"},
		{"control", "codex:s", "status"},
		{"requests", "codex:s"},
		{"answer", "codex:s", "yes"},
		{"permission-mode", "codex:s"},
		{"limit-resume", "resume", "codex:s"},
		{"move", "codex:s", "--to", "elsewhere"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
			fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Send: true, Stream: true, Interrupt: true, Steer: true, Compact: true}}
			app, r := cliFixture(t, fake)
			holdRegistryWriteLock(t, r.Path())
			started := time.Now()
			err := app.Run(append(args, "--timeout", timeout.String()))
			app.Close()
			if elapsed := time.Since(started); elapsed > timeout+time.Second {
				t.Fatalf("returned and closed after %s while the registry was busy (err %v)", elapsed, err)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err=%v, want the command's deadline", err)
			}
		})
	}
}

func TestCommandsOnTheDefaultDeadlineEndAtItWhileTheRegistryIsBusy(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}
	app, r := cliFixture(t, fake)
	app.DefaultTimeout = 300 * time.Millisecond
	holdRegistryWriteLock(t, r.Path())
	started := time.Now()
	err := app.Run([]string{"thread", "archive", "codex:s"})
	app.Close()
	if elapsed := time.Since(started); elapsed > app.DefaultTimeout+time.Second {
		t.Fatalf("thread archive returned and closed after %s while the registry was busy (err %v)", elapsed, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want the default deadline", err)
	}
}
