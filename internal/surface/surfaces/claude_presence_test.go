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

func testProcessStart(t *testing.T) string {
	t.Helper()
	if raw, err := os.ReadFile("/proc/self/stat"); err == nil {
		ticks, ok := linuxProcessStartTicks(string(raw))
		if !ok {
			t.Fatal("unreadable /proc/self/stat")
		}
		return ticks
	}
	start, ok := claudeProcessStart(os.Getpid(), "lstart")
	if !ok {
		t.Fatal("ps lstart unavailable")
	}
	return start
}

func TestClaudeOpenSessionsFollowTheSessionRecord(t *testing.T) {
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	session := surface.Session{ID: "conversation-1", PID: pid}
	record := map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "procStart": testProcessStart(t)}
	writePeerRecord(t, home, pid, record)
	if open := claude.OpenSessions(context.Background(), []surface.Session{session, {ID: "none"}}); !open["conversation-1"] || len(open) != 1 {
		t.Fatalf("live record open=%v, want only conversation-1", open)
	}
	record["procStart"] = "1"
	writePeerRecord(t, home, pid, record)
	if open := claude.OpenSessions(context.Background(), []surface.Session{session}); len(open) != 0 {
		t.Fatalf("record from a reused pid open=%v", open)
	}
	record["procStart"] = testProcessStart(t)
	record["sessionId"] = "conversation-2"
	writePeerRecord(t, home, pid, record)
	if open := claude.OpenSessions(context.Background(), []surface.Session{session}); len(open) != 0 {
		t.Fatalf("record for another conversation open=%v", open)
	}
}

func TestClaudeObserveTakesIdleFromTheSessionRecord(t *testing.T) {
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	path := writeTranscript(t, `{"type":"user","uuid":"resumed","message":{"content":"<task-notification>synthetic</task-notification>"}}`)
	session := &surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Status: surface.StatusBusy, Transcript: path, HasLocal: true}
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "idle", "peerFeatures": []any{"notify_idle"}})
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusIdle || observation.ActiveTurnID != "" {
		t.Fatalf("idle record observation=%+v err=%v", observation, err)
	}
	refreshed, err := claude.LocalStatus(context.Background(), *session)
	if err != nil || refreshed.Status != surface.StatusIdle {
		t.Fatalf("idle record status=%s err=%v", refreshed.Status, err)
	}
	for _, status := range []string{"busy", "shell", "waiting"} {
		writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": status, "peerFeatures": []any{"notify_idle"}})
		observation, err = claude.Observe(context.Background(), session)
		if err != nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID != "resumed" {
			t.Fatalf("%s record observation=%+v err=%v", status, observation, err)
		}
	}
}
