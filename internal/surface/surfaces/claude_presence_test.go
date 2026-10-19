package surfaces

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestPresenceUsesEachRuntimesTruth(t *testing.T) {
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	claude, codex := NewClaude(nil, t.TempDir()), &Codex{}
	tests := []struct {
		name        string
		adapter     surface.Surface
		session     surface.Session
		queueCount  int
		open        bool
		wantCurrent bool
		wantReason  string
	}{
		{name: "open idle Claude", adapter: claude, session: surface.Session{Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}, open: true, wantCurrent: true, wantReason: "open"},
		{name: "closed busy Claude is stale", adapter: claude, session: surface.Session{Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusBusy}, wantCurrent: false},
		{name: "queued closed Claude", adapter: claude, session: surface.Session{Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}, queueCount: 1, wantCurrent: true, wantReason: "queued"},
		{name: "recent Codex", adapter: codex, session: surface.Session{Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: now.Add(-4 * time.Hour)}, wantCurrent: true, wantReason: "recent"},
		{name: "old Codex", adapter: codex, session: surface.Session{Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, LastActive: now.Add(-6 * time.Hour)}, wantCurrent: false},
		{name: "historical Codex", adapter: codex, session: surface.Session{Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.SessionStatus("notLoaded"), LastActive: now.Add(-time.Hour)}, wantCurrent: false},
		{name: "busy Codex", adapter: codex, session: surface.Session{Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, LastActive: now.Add(-48 * time.Hour)}, wantCurrent: true, wantReason: "working"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, reason := surface.PresenceFor(test.adapter, test.session, surface.PresenceInput{QueueCount: test.queueCount, Open: test.open, Now: now, CodexRecentHours: 5})
			if current != test.wantCurrent || reason != test.wantReason {
				t.Fatalf("current=%v reason=%q, want current=%v reason=%q", current, reason, test.wantCurrent, test.wantReason)
			}
		})
	}
}

func TestClaudeOpenSessionsRejectsOtherProcesses(t *testing.T) {
	open := NewClaude(nil, t.TempDir()).OpenSessions(context.Background(), []surface.Session{{ID: "self", PID: os.Getpid()}, {ID: "none"}})
	if len(open) != 0 {
		t.Fatalf("open=%v, want no Claude process", open)
	}
}

func TestParseClaudeOpenProcessesRequiresPositivePIDAndExactClaudeBasename(t *testing.T) {
	got := parseClaudeOpenProcesses("" +
		"0 /Applications/Claude/claude\n" +
		"not-a-pid /Applications/Claude/claude\n" +
		"101 /Applications/Claude Code/claude\n" +
		"102 /usr/local/bin/claude-helper\n" +
		"103 /Applications/Claude Code/Claude\n" +
		"104 /Applications/Claude Code/claude\n")
	if len(got) != 2 || !got[101] || !got[104] {
		t.Fatalf("parsed=%v", got)
	}
}
