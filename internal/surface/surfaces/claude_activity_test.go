package surfaces

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeObserveReportsWhenAWorkingTurnLastProgressedAndTheToolItRuns(t *testing.T) {
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	path := writeTranscript(t, `
{"type":"user","uuid":"prompt","timestamp":"2026-10-23T10:00:00Z","message":{"role":"user","content":"build it"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-23T10:01:00Z","message":{"id":"m1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]}}
{"type":"user","uuid":"r1","timestamp":"2026-10-23T10:01:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-23T10:02:00Z","message":{"id":"m2","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"make"}}]}}`)
	session := &surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Status: surface.StatusBusy, Transcript: path, HasLocal: true}
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "statusUpdatedAt": float64(time.Date(2026, 10, 23, 10, 0, 1, 0, time.UTC).UnixMilli()), "peerFeatures": []any{"notify_idle"}})

	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	want := surface.Activity{BusySince: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC), LastProgressAt: time.Date(2026, 10, 23, 10, 2, 0, 0, time.UTC), Tool: &surface.RunningTool{Name: "Bash", StartedAt: time.Date(2026, 10, 23, 10, 2, 0, 0, time.UTC)}}
	if got := observation.Activity; got == nil || !got.BusySince.Equal(want.BusySince) || !got.LastProgressAt.Equal(want.LastProgressAt) || got.Tool == nil || *got.Tool != *want.Tool || got.NoProcess {
		t.Fatalf("activity=%+v, want %+v", got, want)
	}

	appendTranscript(t, path, `{"type":"user","uuid":"r2","timestamp":"2026-10-23T10:20:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"built"}]}}`+"\n")
	observation, err = claude.Observe(context.Background(), session)
	if err != nil || observation.Activity == nil || observation.Activity.Tool != nil || !observation.Activity.LastProgressAt.Equal(time.Date(2026, 10, 23, 10, 20, 0, 0, time.UTC)) {
		t.Fatalf("after the tool returned activity=%+v err=%v", observation.Activity, err)
	}
	refreshed, err := claude.LocalStatus(context.Background(), *session)
	if err != nil || refreshed.Activity == nil || !refreshed.Activity.LastProgressAt.Equal(observation.Activity.LastProgressAt) {
		t.Fatalf("local status activity=%+v err=%v", refreshed.Activity, err)
	}

	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "idle", "peerFeatures": []any{"notify_idle"}})
	if observation, err = claude.Observe(context.Background(), session); err != nil || observation.Status != surface.StatusIdle || observation.Activity != nil {
		t.Fatalf("idle record observation=%+v err=%v", observation, err)
	}
}

func TestClaudeObserveSaysWhenAnOpenTurnHasNoClaudeProcess(t *testing.T) {
	claude := NewClaude(nil, t.TempDir())
	path := writeTranscript(t, `{"type":"user","uuid":"resumed","timestamp":"2026-10-23T05:07:57Z","message":{"content":"<task-notification>synthetic</task-notification>"}}`)
	session := &surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusBusy, Transcript: path, HasLocal: true}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.Activity == nil || !observation.Activity.NoProcess || !observation.Activity.LastProgressAt.Equal(time.Date(2026, 10, 23, 5, 7, 57, 0, time.UTC)) {
		t.Fatalf("observation=%+v activity=%+v err=%v", observation, observation.Activity, err)
	}
}
