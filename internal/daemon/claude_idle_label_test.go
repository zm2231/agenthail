package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const idleCompactionThenAnEmptyLastReply = `{"type":"user","uuid":"u1","promptId":"p1","timestamp":"2026-11-07T05:54:18Z","message":{"role":"user","content":"first"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-11-07T05:54:34Z","message":{"id":"m1","model":"claude-fixture","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"one"}],"usage":{"input_tokens":3,"cache_read_input_tokens":232000,"output_tokens":20}}}
{"type":"system","subtype":"stop_hook_summary","hookCount":2,"hookErrors":[],"preventedContinuation":false,"timestamp":"2026-11-07T05:54:36Z"}
{"type":"system","subtype":"compact_boundary","content":"Conversation compacted","isMeta":false,"level":"info","timestamp":"2026-11-07T06:45:34Z","uuid":"b1","compactMetadata":{"trigger":"manual","preTokens":232004,"durationMs":42075}}
{"type":"user","uuid":"s1","promptId":"p2","isCompactSummary":true,"timestamp":"2026-11-07T06:45:34Z","message":{"role":"user","content":"This session is being continued from a previous conversation."}}
{"type":"user","uuid":"k1","promptId":"p2","isMeta":true,"timestamp":"2026-11-07T06:44:52Z","message":{"role":"user","content":"<local-command-caveat>The command below was run directly in Claude Code.</local-command-caveat>"}}
{"type":"user","uuid":"c1","promptId":"p2","timestamp":"2026-11-07T06:44:52Z","message":{"role":"user","content":"<command-name>/compact</command-name>\n            <command-message>compact</command-message>\n            <command-args></command-args>"}}
{"type":"user","uuid":"o1","promptId":"p2","timestamp":"2026-11-07T06:45:34Z","message":{"role":"user","content":"<local-command-stdout>Compacted </local-command-stdout>"}}
{"type":"user","uuid":"u2","promptId":"p3","isMeta":true,"origin":{"kind":"peer","name":"lead"},"timestamp":"2026-11-07T07:48:04Z","message":{"role":"user","content":"Another Claude session sent a message"}}
{"type":"assistant","uuid":"a2","timestamp":"2026-11-07T07:48:35Z","message":{"id":"m2","model":"claude-fixture","role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"acknowledged"},{"type":"tool_use","id":"t2","name":"mcp__agenthail__agenthail_end_turn","input":{"headline":"waiting","needs":"nothing"}}],"usage":{"input_tokens":3,"cache_read_input_tokens":16000,"output_tokens":30}}}
{"type":"user","uuid":"r2","timestamp":"2026-11-07T07:48:38Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"{\"needs\":\"nothing\",\"ok\":true}"}]}}
{"type":"api-request","timestamp":"2026-11-07T07:48:38Z"}
{"type":"system","subtype":"stop_hook_summary","hookCount":2,"hookErrors":[],"preventedContinuation":false,"stopReason":"","hasOutput":false,"level":"suggestion","timestamp":"2026-11-07T07:48:44Z"}
`

func TestAnIdleCompactionIsRecordedOnTheNextTurnWhenThatTurnsLastReplyWasEmpty(t *testing.T) {
	home := t.TempDir()
	id := "idle-label"
	transcript := filepath.Join(home, ".claude", "projects", "work", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(idleCompactionThenAnEmptyLastReply), 0o600); err != nil {
		t.Fatal(err)
	}
	r := hostRegistry(t)
	session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: transcript}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if outcome, err := r.QueueIdleCompaction(id, time.Date(2026, 11, 7, 5, 54, 18, 0, time.UTC)); err != nil || outcome != registry.IdleCompactionQueued {
		t.Fatalf("queue idle compaction: %v %v", outcome, err)
	}
	history, err := r.ListHistory(10, id)
	if err != nil {
		t.Fatal(err)
	}
	queued := slices.IndexFunc(history, func(entry registry.HistoryEntry) bool { return entry.Kind == "queued" && entry.QueueID != 0 })
	if queued < 0 {
		t.Fatalf("no queued idle compact in %+v", history)
	}
	if err := r.RecordIdleCompactionRequest(history[queued].QueueID, "c1"); err != nil {
		t.Fatal(err)
	}
	claude := surfaces.NewClaude(nil, home)
	d := newTestDaemon(t, r, []surface.Surface{claude})
	page, err := d.readProviderHistoryPage(context.Background(), &session, claude, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var done []surface.TimelineItem
	for _, item := range page.Items {
		if item.Kind == "done" && item.TurnID == "u2" {
			done = append(done, item)
		}
	}
	if len(done) != 1 || done[0].Turn == nil {
		t.Fatalf("the turn after the idle compaction ended %d times: %+v", len(done), done)
	}
	var triggers []string
	for _, compaction := range done[0].Turn.Compactions {
		triggers = append(triggers, compaction.Trigger)
	}
	if !slices.Equal(triggers, []string{surface.CompactionIdle}) {
		t.Fatalf("compactions before the turn = %v, want the one idle compaction", triggers)
	}
	if done[0].Turn.Usage == nil || done[0].Turn.Usage.OutputTokens != 30 {
		t.Fatalf("the turn lost its usage: %+v", done[0].Turn.Usage)
	}
}
