package codexsurface

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func TestCodexListAndLocalStatusShareTranscriptPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	surfacetest.AppendLine(t, path, `{"type":"event_msg","payload":{"type":"task_started"}}`)
	codex := &Codex{}
	listed := surface.Session{ID: "synthetic", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Transcript: path}
	codex.reconcileLocalStatus(context.Background(), &listed)
	local, err := codex.LocalStatus(context.Background(), surface.Session{ID: "synthetic", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Transcript: path})
	if err != nil || listed.Status != surface.StatusBusy || local.Status != listed.Status {
		t.Fatalf("listed=%s local=%s err=%v", listed.Status, local.Status, err)
	}
	surfacetest.AppendLine(t, path, `{"type":"event_msg","payload":{"type":"task_complete"}}`)
	listed.Status = surface.StatusBusy
	codex.reconcileLocalStatus(context.Background(), &listed)
	if listed.Status != surface.StatusIdle {
		t.Fatalf("provider busy overrode completed transcript: %s", listed.Status)
	}
}

func TestCodexLocalStatusFollowsTranscriptLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	surfacetest.AppendLine(t, path, `{"type":"event_msg","payload":{"type":"task_complete"}}`)
	codex := &Codex{}
	session := surface.Session{ID: "synthetic", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Transcript: path}
	if files := codex.LocalStatusFiles(session); len(files) != 1 || files[0] != path {
		t.Fatalf("files=%v", files)
	}

	surfacetest.AppendLine(t, path, `{"type":"event_msg","payload":{"type":"task_started"}}`)
	busy, err := codex.LocalStatus(context.Background(), session)
	if err != nil || busy.Status != surface.StatusBusy || busy.LastActive.IsZero() {
		t.Fatalf("busy=%+v err=%v", busy, err)
	}

	surfacetest.AppendLine(t, path, `{"type":"response_item","payload":{"type":"message"}}`)
	surfacetest.AppendLine(t, path, `{"type":"event_msg","payload":{"type":"turn_aborted"}}`)
	idle, err := codex.LocalStatus(context.Background(), busy)
	if err != nil || idle.Status != surface.StatusIdle {
		t.Fatalf("idle=%+v err=%v", idle, err)
	}

	unknown := filepath.Join(t.TempDir(), "rollout-empty.jsonl")
	surfacetest.AppendLine(t, unknown, `{"type":"session_meta","payload":{}}`)
	kept, err := codex.LocalStatus(context.Background(), surface.Session{ID: "provider", Status: surface.StatusBusy, Transcript: unknown})
	if err != nil || kept.Status != surface.StatusBusy {
		t.Fatalf("transcript without lifecycle replaced provider status: %+v err=%v", kept, err)
	}
	if none, err := codex.LocalStatus(context.Background(), surface.Session{ID: "none", Status: surface.StatusBusy}); err != nil || none.Status != surface.StatusBusy {
		t.Fatalf("session without transcript changed status: %+v err=%v", none, err)
	}
}
