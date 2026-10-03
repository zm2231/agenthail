package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type callerCLISurface struct {
	*cliSurface
	caller       *surface.Session
	callerFound  bool
	callerErr    error
	ancestorPIDs []int
}

func (s *callerCLISurface) ResolveCaller(_ context.Context, ancestorPIDs []int) (*surface.Session, bool, error) {
	s.ancestorPIDs = append([]int(nil), ancestorPIDs...)
	return s.caller, s.callerFound, s.callerErr
}

func withProcessSnapshot(t *testing.T, snapshot []processIdentity) {
	t.Helper()
	previous := processSnapshot
	processSnapshot = func(context.Context) ([]processIdentity, error) { return snapshot, nil }
	t.Cleanup(func() { processSnapshot = previous })
}

func TestSourceIdentityUsesExplicitSenderBeforeEnvironment(t *testing.T) {
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "older")
	t.Setenv("CLAUDE_SESSION_ID", "")
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"older": {ID: "older", Surface: surface.KindCodex}, "explicit": {ID: "explicit", Surface: surface.KindCodex}}}
	app := App{Surfaces: []SurfaceEntry{{Name: "codex", Surface: fake}}}
	for selector, want := range map[string]string{"": "older", "codex:explicit": "explicit"} {
		got, err := app.sourceSessionID(context.Background(), selector)
		if err != nil || got != want {
			t.Fatalf("selector=%q got=%q err=%v", selector, got, err)
		}
	}
	if _, err := app.sourceSessionID(context.Background(), "codex:missing"); err == nil {
		t.Fatal("unresolvable sender accepted")
	}
}

func TestSourceIdentityUsesRegisteredSessionWithoutOpeningItsTransport(t *testing.T) {
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "current")
	t.Setenv("CLAUDE_SESSION_ID", "")
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterSession(surface.Session{ID: "current", Surface: surface.KindCodex, Name: "voice source"}); err != nil {
		t.Fatal(err)
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}}
	app := App{Registry: store, Surfaces: []SurfaceEntry{{Name: "codex", Surface: fake}}}
	got, err := app.sourceSessionID(context.Background(), "")
	if err != nil || got != "current" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestSourceIdentityResolvesNestedClaudeCallerWithoutEnvironment(t *testing.T) {
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
	self := os.Getpid()
	claudePID := self + 1000
	withProcessSnapshot(t, []processIdentity{
		{pid: self, ppid: self + 1, uid: uint32(os.Getuid())},
		{pid: self + 1, ppid: claudePID, uid: uint32(os.Getuid())},
		{pid: claudePID, ppid: 1, uid: uint32(os.Getuid())},
	})
	fake := &callerCLISurface{
		cliSurface:  &cliSurface{kind: surface.KindClaude},
		caller:      &surface.Session{ID: "session-native", Surface: surface.KindClaude, PID: claudePID, Transport: "uds"},
		callerFound: true,
	}
	app := App{Surfaces: []SurfaceEntry{{Name: "claude", Surface: fake}}}
	got, err := app.sourceSessionID(context.Background(), "")
	if err != nil || got != "session-native" {
		t.Fatalf("source=%q err=%v", got, err)
	}
	if len(fake.ancestorPIDs) != 3 || fake.ancestorPIDs[2] != claudePID {
		t.Fatalf("ancestry=%v", fake.ancestorPIDs)
	}
}

func TestSourceIdentityRejectsRecognizedClaudeCallerWithoutValidatedEndpoint(t *testing.T) {
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
	self := os.Getpid()
	withProcessSnapshot(t, []processIdentity{{pid: self, ppid: 1, uid: uint32(os.Getuid())}})
	fake := &callerCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}, callerErr: errors.New("Claude ancestor PID has no validated messaging endpoint")}
	app := App{Surfaces: []SurfaceEntry{{Name: "claude", Surface: fake}}}
	if _, err := app.sourceSessionID(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "validated messaging endpoint") {
		t.Fatalf("err=%v", err)
	}
}

func TestSourceIdentityLeavesStandaloneCLICallerUnbound(t *testing.T) {
	t.Setenv("AGENTHAIL_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
	self := os.Getpid()
	withProcessSnapshot(t, []processIdentity{{pid: self, ppid: 1, uid: uint32(os.Getuid())}})
	fake := &callerCLISurface{cliSurface: &cliSurface{kind: surface.KindClaude}}
	app := App{Surfaces: []SurfaceEntry{{Name: "claude", Surface: fake}}}
	got, err := app.sourceSessionID(context.Background(), "")
	if err != nil || got != "" {
		t.Fatalf("source=%q err=%v", got, err)
	}
}

func TestSourceIdentityKeepsExplicitBindingAheadOfClaudeCaller(t *testing.T) {
	self := os.Getpid()
	withProcessSnapshot(t, []processIdentity{{pid: self, ppid: 1, uid: uint32(os.Getuid())}})
	fake := &callerCLISurface{
		cliSurface:  &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"explicit": {ID: "explicit", Surface: surface.KindClaude}}},
		caller:      &surface.Session{ID: "inferred", Surface: surface.KindClaude},
		callerFound: true,
	}
	app := App{Surfaces: []SurfaceEntry{{Name: "claude", Surface: fake}}}
	got, err := app.sourceSessionID(context.Background(), "claude:explicit")
	if err != nil || got != "explicit" {
		t.Fatalf("source=%q err=%v", got, err)
	}
	if len(fake.ancestorPIDs) != 0 {
		t.Fatalf("caller discovery ran for explicit binding: %v", fake.ancestorPIDs)
	}
}
