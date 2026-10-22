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
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestCodexLocalStatusFollowsTranscriptLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	appendLine(t, path, `{"type":"event_msg","payload":{"type":"task_complete"}}`)
	codex := &Codex{}
	session := surface.Session{ID: "synthetic", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Transcript: path}
	if files := codex.LocalStatusFiles(session); len(files) != 2 || files[0] != codexPendingModelPath(session.ID) || files[1] != path {
		t.Fatalf("files=%v", files)
	}

	appendLine(t, path, `{"type":"event_msg","payload":{"type":"task_started"}}`)
	busy, err := codex.LocalStatus(context.Background(), session)
	if err != nil || busy.Status != surface.StatusBusy || busy.LastActive.IsZero() {
		t.Fatalf("busy=%+v err=%v", busy, err)
	}

	appendLine(t, path, `{"type":"response_item","payload":{"type":"message"}}`)
	appendLine(t, path, `{"type":"event_msg","payload":{"type":"turn_aborted"}}`)
	idle, err := codex.LocalStatus(context.Background(), busy)
	if err != nil || idle.Status != surface.StatusIdle {
		t.Fatalf("idle=%+v err=%v", idle, err)
	}

	unknown := filepath.Join(t.TempDir(), "rollout-empty.jsonl")
	appendLine(t, unknown, `{"type":"session_meta","payload":{}}`)
	kept, err := codex.LocalStatus(context.Background(), surface.Session{ID: "provider", Status: surface.StatusBusy, Transcript: unknown})
	if err != nil || kept.Status != surface.StatusBusy {
		t.Fatalf("transcript without lifecycle replaced provider status: %+v err=%v", kept, err)
	}
	if none, err := codex.LocalStatus(context.Background(), surface.Session{ID: "none", Status: surface.StatusBusy}); err != nil || none.Status != surface.StatusBusy {
		t.Fatalf("session without transcript changed status: %+v err=%v", none, err)
	}
}

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

func TestCodexListAndLocalStatusShareTranscriptPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	appendLine(t, path, `{"type":"event_msg","payload":{"type":"task_started"}}`)
	codex := &Codex{}
	listed := surface.Session{ID: "synthetic", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Transcript: path}
	codex.reconcileLocalStatus(&listed)
	local, err := codex.LocalStatus(context.Background(), surface.Session{ID: "synthetic", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Transcript: path})
	if err != nil || listed.Status != surface.StatusBusy || local.Status != listed.Status {
		t.Fatalf("listed=%s local=%s err=%v", listed.Status, local.Status, err)
	}
	appendLine(t, path, `{"type":"event_msg","payload":{"type":"task_complete"}}`)
	listed.Status = surface.StatusBusy
	codex.reconcileLocalStatus(&listed)
	if listed.Status != surface.StatusIdle {
		t.Fatalf("provider busy overrode completed transcript: %s", listed.Status)
	}
}

func TestClaudeLocalStatusDatesWorkingSessionByTranscriptWrites(t *testing.T) {
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	started := time.Now().Add(-40 * time.Minute).Truncate(time.Millisecond)
	transcript := filepath.Join(t.TempDir(), "conversation-1.jsonl")
	appendLine(t, transcript, `{"type":"user","uuid":"u1","timestamp":"`+started.UTC().Format(time.RFC3339Nano)+`","message":{"role":"user","content":"work"}}`)
	written := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := os.Chtimes(transcript, written, written); err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "updatedAt": float64(started.UnixMilli())}
	writePeerRecord(t, home, pid, record)
	session := surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: pid, Status: surface.StatusBusy, Transcript: transcript, HasLocal: true}

	busy, err := claude.LocalStatus(context.Background(), session)
	if err != nil || busy.Status != surface.StatusBusy || !busy.LastActive.Equal(written) {
		t.Fatalf("busy status=%s lastActive=%s want %s err=%v", busy.Status, busy.LastActive, written, err)
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
