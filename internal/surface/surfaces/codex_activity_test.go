package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func codexRolloutRecord(at time.Time, kind, payload string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":%q,"payload":%s}`, at.UTC().Format(time.RFC3339Nano), kind, payload)
}

func codexWorkingSession(path string, status surface.SessionStatus) surface.Session {
	return surface.Session{ID: "synthetic", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: status, Transcript: path}
}

func TestCodexLocalStatusReportsWhenAWorkingTurnLastProgressedAndTheToolItRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	started := time.Now().Add(-6 * time.Minute).Truncate(time.Millisecond)
	appendLine(t, path, codexRolloutRecord(started.Add(-time.Hour), "event_msg", `{"type":"task_started"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(-time.Hour), "response_item", `{"type":"custom_tool_call","name":"exec","call_id":"old"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(-50*time.Minute), "event_msg", `{"type":"task_complete"}`))
	appendLine(t, path, codexRolloutRecord(started, "event_msg", `{"type":"task_started"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(time.Minute), "response_item", `{"type":"custom_tool_call","name":"exec","call_id":"c1"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(2*time.Minute), "response_item", `{"type":"function_call","name":"wait","call_id":"c2"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(3*time.Minute), "response_item", `{"type":"function_call_output","call_id":"c2"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(4*time.Minute), "event_msg", `{"type":"token_count"}`))
	codex := &Codex{}

	local, err := codex.LocalStatus(context.Background(), codexWorkingSession(path, surface.StatusIdle))
	if err != nil || local.Status != surface.StatusBusy {
		t.Fatalf("local=%+v err=%v", local, err)
	}
	want := surface.Activity{BusySince: started, LastProgressAt: started.Add(4 * time.Minute), Tool: &surface.RunningTool{Name: "exec", StartedAt: started.Add(time.Minute)}}
	if local.Activity == nil || !local.Activity.BusySince.Equal(want.BusySince) || !local.Activity.LastProgressAt.Equal(want.LastProgressAt) || local.Activity.Tool == nil || local.Activity.Tool.Name != want.Tool.Name || !local.Activity.Tool.StartedAt.Equal(want.Tool.StartedAt) || local.Activity.NoProcess {
		t.Fatalf("activity=%+v tool=%+v, want %+v tool=%+v", local.Activity, toolOf(local.Activity), want, want.Tool)
	}
	listed := codexWorkingSession(path, surface.StatusIdle)
	codex.reconcileLocalStatus(&listed)
	if !surface.SameActivity(listed.Activity, local.Activity) {
		t.Fatalf("discovery and the status pass disagree: listed=%+v local=%+v", listed.Activity, local.Activity)
	}

	appendLine(t, path, codexRolloutRecord(started.Add(5*time.Minute), "response_item", `{"type":"custom_tool_call_output","call_id":"c1"}`))
	settled, err := codex.LocalStatus(context.Background(), local)
	if err != nil || settled.Status != surface.StatusBusy || settled.Activity == nil || settled.Activity.Tool != nil || !settled.Activity.LastProgressAt.Equal(started.Add(5*time.Minute)) {
		t.Fatalf("a tool with its output still reads as running: %+v err=%v", settled.Activity, err)
	}

	appendLine(t, path, codexRolloutRecord(started.Add(6*time.Minute), "event_msg", `{"type":"task_complete"}`))
	done, err := codex.LocalStatus(context.Background(), settled)
	if err != nil || done.Status != surface.StatusIdle || done.Activity != nil {
		t.Fatalf("a finished turn kept its activity: status=%s activity=%+v err=%v", done.Status, done.Activity, err)
	}
}

func TestAHungCodexTurnTheRuntimeStillRunsReadsAsQuiet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	started := time.Now().Add(-90 * time.Minute).Truncate(time.Millisecond)
	appendLine(t, path, codexRolloutRecord(started, "event_msg", `{"type":"task_started"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(2*time.Minute), "response_item", `{"type":"custom_tool_call","name":"exec","call_id":"build"}`))
	written := started.Add(2 * time.Minute)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	codex := &Codex{}

	working := codexWorkingSession(path, surface.StatusBusy)
	codex.reconcileLocalStatus(&working)
	if working.Status != surface.StatusBusy || working.Activity == nil || !working.Activity.LastProgressAt.Equal(written) || working.Activity.Tool == nil || working.Activity.Tool.Name != "exec" {
		t.Fatalf("a runtime-busy session with a quiet rollout: status=%s activity=%+v", working.Status, working.Activity)
	}

	idle := codexWorkingSession(path, surface.StatusIdle)
	idle.Activity = &surface.Activity{BusySince: started}
	codex.reconcileLocalStatus(&idle)
	if idle.Status != surface.StatusIdle || idle.Activity != nil {
		t.Fatalf("a stale open turn the runtime is not running read as working: status=%s activity=%+v", idle.Status, idle.Activity)
	}
}

func TestACodexTurnWithoutTimestampsDatesProgressByTheRolloutWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	appendLine(t, path, `{"type":"event_msg","payload":{"type":"task_started"}}`)
	appendLine(t, path, `{"type":"response_item","payload":{"type":"function_call","name":"js","call_id":"c1"}}`)
	written := time.Now().Add(-3 * time.Minute).Truncate(time.Second)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	local, err := (&Codex{}).LocalStatus(context.Background(), codexWorkingSession(path, surface.StatusIdle))
	if err != nil || local.Status != surface.StatusBusy || local.Activity == nil || !local.Activity.LastProgressAt.Equal(written) || local.Activity.Tool == nil || local.Activity.Tool.Name != "js" {
		t.Fatalf("status=%s activity=%+v err=%v", local.Status, local.Activity, err)
	}
}

func toolOf(activity *surface.Activity) *surface.RunningTool {
	if activity == nil {
		return nil
	}
	return activity.Tool
}

func TestACodexTurnReadsTheSameAcrossTheRolloutReadChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	started := time.Now().Add(-5 * time.Minute).Truncate(time.Millisecond)
	appendLine(t, path, codexRolloutRecord(started.Add(-time.Minute), "response_item", `{"type":"function_call","name":"earlier","call_id":"before"}`))
	appendLine(t, path, codexRolloutRecord(started, "event_msg", `{"type":"task_started"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(time.Second), "response_item", `{"type":"custom_tool_call","name":"exec","call_id":"long"}`))
	text := strings.Repeat("x", 1500)
	for index := 0; int64(index*1600) < 3*codexStatusReadChunk; index++ {
		appendLine(t, path, codexRolloutRecord(started.Add(2*time.Second), "response_item", `{"type":"message","content":"`+text+`"}`))
	}
	last := started.Add(3 * time.Second)
	appendLine(t, path, codexRolloutRecord(last, "event_msg", `{"type":"token_count"}`))
	if info, err := os.Stat(path); err != nil || info.Size() <= 2*codexStatusReadChunk {
		t.Fatalf("the fixture must span several read chunks: %v %v", info, err)
	}

	local, err := (&Codex{}).LocalStatus(context.Background(), codexWorkingSession(path, surface.StatusIdle))
	if err != nil || local.Status != surface.StatusBusy || local.Activity == nil || !local.Activity.BusySince.Equal(started) || !local.Activity.LastProgressAt.Equal(last) || local.Activity.Tool == nil || local.Activity.Tool.Name != "exec" {
		t.Fatalf("status=%s activity=%+v tool=%+v err=%v", local.Status, local.Activity, toolOf(local.Activity), err)
	}
}
