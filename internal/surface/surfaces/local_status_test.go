package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func writePeerRecord(t *testing.T, home string, pid int, record map[string]any) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeLocalStatusRereadsPeerRecord(t *testing.T) {
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	updated := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	record := map[string]any{"pid": pid, "sessionId": "conversation-1", "bridgeSessionId": "session_synthetic", "status": "busy", "updatedAt": float64(updated.UnixMilli()), "peerFeatures": []any{"notify_idle"}}
	writePeerRecord(t, home, pid, record)
	session := surface.Session{ID: "conversation-1", Bridge: "session_synthetic", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Status: surface.StatusIdle}
	if files := claude.LocalStatusFiles(session); len(files) != 1 || files[0] != filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json") {
		t.Fatalf("files=%v", files)
	}

	busy, err := claude.LocalStatus(context.Background(), session)
	if err != nil || busy.Status != surface.StatusBusy || !busy.LastActive.Equal(updated) {
		t.Fatalf("busy=%+v err=%v", busy, err)
	}

	record["status"] = "idle"
	record["updatedAt"] = float64(updated.Add(time.Minute).UnixMilli())
	writePeerRecord(t, home, pid, record)
	idle, err := claude.LocalStatus(context.Background(), busy)
	if err != nil || idle.Status != surface.StatusIdle {
		t.Fatalf("idle=%+v err=%v", idle, err)
	}

	record["bridgeSessionId"] = "session_other"
	record["sessionId"] = "conversation-2"
	writePeerRecord(t, home, pid, record)
	if _, err := claude.LocalStatus(context.Background(), idle); err == nil {
		t.Fatal("reused pid record was applied to another session")
	}
}

// A working session's last activity is the progress its activity reports, so
// a row never shows recent activity beside a note that the turn went quiet.
func TestClaudeLocalStatusDatesWorkingSessionByItsLastProgress(t *testing.T) {
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	started := time.Now().Add(-40 * time.Minute).Truncate(time.Millisecond)
	progressed := time.Now().Add(-15 * time.Minute).Truncate(time.Millisecond)
	transcript := filepath.Join(t.TempDir(), "conversation-1.jsonl")
	surfacetest.AppendLine(t, transcript, `{"type":"user","uuid":"u1","timestamp":"`+started.UTC().Format(time.RFC3339Nano)+`","message":{"role":"user","content":"work"}}`)
	surfacetest.AppendLine(t, transcript, `{"type":"assistant","uuid":"a1","timestamp":"`+progressed.UTC().Format(time.RFC3339Nano)+`","message":{"id":"m1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}]}}`)
	written := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := os.Chtimes(transcript, written, written); err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "updatedAt": float64(started.UnixMilli()), "peerFeatures": []any{"notify_idle"}}
	writePeerRecord(t, home, pid, record)
	session := surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: pid, Status: surface.StatusBusy, Transcript: transcript, HasLocal: true}

	busy, err := claude.LocalStatus(context.Background(), session)
	if err != nil || busy.Status != surface.StatusBusy || busy.Activity == nil || !busy.LastActive.Equal(progressed) || !busy.LastActive.Equal(busy.Activity.LastProgressAt) {
		t.Fatalf("busy status=%s lastActive=%s activity=%+v want %s err=%v", busy.Status, busy.LastActive, busy.Activity, progressed, err)
	}

	finished := written.Add(-30 * time.Second)
	record["status"] = "idle"
	record["peerFeatures"] = []any{"notify_idle"}
	record["updatedAt"] = float64(finished.UnixMilli())
	writePeerRecord(t, home, pid, record)
	idle, err := claude.LocalStatus(context.Background(), busy)
	if err != nil || idle.Status == surface.StatusBusy || !idle.LastActive.Equal(finished) {
		t.Fatalf("idle status=%s lastActive=%s want record time %s err=%v", idle.Status, idle.LastActive, finished, err)
	}
}
