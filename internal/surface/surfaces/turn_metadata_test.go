package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func count(n int64) *int64 { return &n }

func turnJSON(t *testing.T, turn *surface.TurnMetadata) string {
	t.Helper()
	encoded, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func wantTurn(t *testing.T, label string, got, want *surface.TurnMetadata) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s turn:\n got %s\nwant %s", label, turnJSON(t, got), turnJSON(t, want))
	}
}

const codexTurnRecords = `{"timestamp":"2026-10-19T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-10-19T10:00:00Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-fixture"}}
{"timestamp":"2026-10-19T10:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"build it"}}
{"timestamp":"2026-10-19T10:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":5,"output_tokens":20,"reasoning_output_tokens":8,"total_tokens":120}}}}
`

const codexTurnRest = `{"timestamp":"2026-10-19T10:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":120},"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":5,"output_tokens":20,"reasoning_output_tokens":8,"total_tokens":120}}}}
{"timestamp":"2026-10-19T10:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":300},"last_token_usage":{"input_tokens":150,"cached_input_tokens":100,"cache_write_input_tokens":0,"output_tokens":30,"reasoning_output_tokens":2,"total_tokens":180}}}}
{"timestamp":"2026-10-19T10:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"built"}]}}
{"timestamp":"2026-10-19T10:00:04Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","duration_ms":4200}}
{"timestamp":"2026-10-19T10:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-10-19T10:01:00Z","type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt-fixture"}}
{"timestamp":"2026-10-19T10:01:00Z","type":"event_msg","payload":{"type":"user_message","message":"and test it"}}
{"timestamp":"2026-10-19T10:01:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":350},"last_token_usage":{"input_tokens":40,"output_tokens":10,"total_tokens":50}}}}
{"timestamp":"2026-10-19T10:01:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"stopping"}]}}
{"timestamp":"2026-10-19T10:01:03Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-2","parent_turn_id":"turn-1"}}
`

var (
	codexCompletedTurn = &surface.TurnMetadata{V: 1, State: "completed", Model: "gpt-fixture", DurationMs: count(4200), Usage: &surface.TurnUsage{InputTokens: 250, CachedInputTokens: 140, CacheWriteTokens: 5, OutputTokens: 50, ReasoningTokens: count(10)}}
	codexAbortedTurn   = &surface.TurnMetadata{V: 1, State: "interrupted", Model: "gpt-fixture", Usage: &surface.TurnUsage{InputTokens: 40, OutputTokens: 10}, Unavailable: []string{"durationMs", "usage.reasoningTokens"}}
)

func TestCodexTranscriptReportsEachTurnsModelUsageAndDuration(t *testing.T) {
	path := timelineFixture(t, codexTurnRecords+codexTurnRest)
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	read, err := codex.ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	wantTurn(t, "completed", read.Exchanges[0].Turn, codexCompletedTurn)
	wantTurn(t, "aborted", read.Exchanges[1].Turn, codexAbortedTurn)
}

func TestCodexStreamResumedMidTurnCountsTheWholeTurn(t *testing.T) {
	path := timelineFixture(t, codexTurnRecords)
	offset := int64(len(codexTurnRecords))
	appendRecords(t, path, codexTurnRest)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var turns []*surface.TurnMetadata
	err := (&Codex{}).Stream(ctx, &surface.Session{ID: "thread", Transcript: path, TranscriptOffset: offset, TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			turns = append(turns, event.Turn)
			if len(turns) == 2 {
				cancel()
			}
		}
	}, 2*time.Second)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("done turns=%d", len(turns))
	}
	wantTurn(t, "completed", turns[0], codexCompletedTurn)
	wantTurn(t, "aborted", turns[1], codexAbortedTurn)
}

func TestCodexTurnWhoseStartIsOutOfReachHasNoUsage(t *testing.T) {
	limit := turnScanLimit
	turnScanLimit = 1
	t.Cleanup(func() { turnScanLimit = limit })
	path := timelineFixture(t, codexTurnRecords)
	appendRecords(t, path, codexTurnRest)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var first *surface.TurnMetadata
	err := (&Codex{}).Stream(ctx, &surface.Session{ID: "thread", Transcript: path, TranscriptOffset: int64(len(codexTurnRecords)), TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "done" && first == nil {
			first = event.Turn
			cancel()
		}
	}, 2*time.Second)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wantTurn(t, "joined midway", first, &surface.TurnMetadata{V: 1, State: "completed", DurationMs: count(4200), Unavailable: []string{"model", "usage"}})
}

const claudeTurnHead = `{"type":"user","uuid":"u1","timestamp":"2026-10-19T10:00:00Z","message":{"content":"fix it"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:02Z","message":{"id":"m1","model":"claude-fixture","content":[{"type":"thinking","thinking":"","signature":"sig"}],"usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T10:00:03Z","message":{"id":"m1","model":"claude-fixture","content":[{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"go test"}}],"usage":{"input_tokens":3,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":40,"output_tokens_details":{"thinking_tokens":12}}}}
`

const claudeTurnTail = `{"type":"user","uuid":"r1","timestamp":"2026-10-19T10:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-10-19T10:00:07Z","message":{"id":"m2","model":"claude-fixture","stop_reason":"end_turn","content":[{"type":"text","text":"fixed"}],"usage":{"input_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":1100,"output_tokens":15,"output_tokens_details":{"thinking_tokens":0}}}}
{"type":"system","subtype":"turn_duration","uuid":"s1","timestamp":"2026-10-19T10:00:07.5Z"}
{"type":"user","uuid":"u2","timestamp":"2026-10-19T10:01:00Z","message":{"content":"and the docs"}}
{"type":"assistant","uuid":"a4","timestamp":"2026-10-19T10:01:02Z","message":{"id":"m3","model":"claude-fixture","content":[{"type":"text","text":"writing"}],"usage":{"input_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":50,"output_tokens":9}}}
{"type":"assistant","uuid":"a5","timestamp":"2026-10-19T10:01:04Z","message":{"id":"m4","model":"<synthetic>","content":[{"type":"text","text":"API Error: overloaded"}]}}
{"type":"user","uuid":"interrupt","timestamp":"2026-10-19T10:01:05Z","message":{"content":"[Request interrupted by user]"}}
`

var (
	claudeCompletedTurn   = &surface.TurnMetadata{V: 1, State: "completed", Model: "claude-fixture", DurationMs: count(7000), Usage: &surface.TurnUsage{InputTokens: 2228, CachedInputTokens: 2100, CacheWriteTokens: 120, OutputTokens: 55, ReasoningTokens: count(12)}}
	claudeInterruptedTurn = &surface.TurnMetadata{V: 1, State: "interrupted", Model: "claude-fixture", DurationMs: count(5000), Usage: &surface.TurnUsage{InputTokens: 57, CachedInputTokens: 50, OutputTokens: 9}, Unavailable: []string{"usage.reasoningTokens"}}
)

func TestClaudeTranscriptReportsEachTurnsModelUsageAndDuration(t *testing.T) {
	path := timelineFixture(t, claudeTurnHead+claudeTurnTail)
	read, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	wantTurn(t, "completed", read.Exchanges[0].Turn, claudeCompletedTurn)
	wantTurn(t, "interrupted", read.Exchanges[1].Turn, claudeInterruptedTurn)
}

func TestClaudeStreamResumedMidTurnCountsTheWholeTurn(t *testing.T) {
	path := writeTranscript(t, claudeTurnHead)
	session := seededClaudeStreamSession(t, path, "")
	appendRecords(t, path, claudeTurnTail)
	turns := map[string]*surface.TurnMetadata{}
	err := NewClaude(nil, t.TempDir()).Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			turns[event.TurnID] = event.Turn
		}
	}, 150*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) || len(turns) != 2 {
		t.Fatalf("err=%v turns=%v", err, turns)
	}
	wantTurn(t, "completed", turns["u1"], claudeCompletedTurn)
	wantTurn(t, "interrupted", turns["u2"], claudeInterruptedTurn)
}

func TestATurnStillInProgressIsRunningAndOneWithoutAnEndIsUnknown(t *testing.T) {
	path := timelineFixture(t, claudeTurnHead)
	read, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusBusy}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 1 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	wantTurn(t, "in progress", read.Exchanges[0].Turn, &surface.TurnMetadata{V: 1, State: "running", Unavailable: []string{"model", "durationMs", "usage"}})

	path = timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
{"timestamp":"2026-09-07T12:02:00Z","type":"event_msg","payload":{"type":"user_message","message":"again"}}
`)
	read, err = (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	unknown := &surface.TurnMetadata{V: 1, State: "unknown", Unavailable: []string{"state", "model", "durationMs", "usage"}}
	wantTurn(t, "superseded", read.Exchanges[0].Turn, unknown)
	wantTurn(t, "idle tail", read.Exchanges[1].Turn, unknown)
	if strings.Contains(turnJSON(t, unknown), "inputTokens") {
		t.Fatal("unavailable usage must not read as zero tokens")
	}

	path = timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-a"}}
{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
{"timestamp":"2026-09-07T12:02:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-b"}}
{"timestamp":"2026-09-07T12:02:05Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-b","duration_ms":5000}}
`)
	read, err = (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 1 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	wantTurn(t, "turn without an end before a silent turn", read.Exchanges[0].Turn, unknown)
}

func TestManagedCodexTurnsCarryWhatTheirRolloutRecorded(t *testing.T) {
	path := timelineFixture(t, codexTurnRecords+codexTurnRest+`{"timestamp":"2026-10-19T10:02:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-3"}}
{"timestamp":"2026-10-19T10:02:00Z","type":"event_msg","payload":{"type":"user_message","message":"one more"}}
{"timestamp":"2026-10-19T10:02:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"no end in the rollout"}]}}
`)
	session := managedSession()
	session.Transcript = path
	turn := func(id, status, text string) any {
		return map[string]any{"id": id, "status": status, "items": []any{agentMessage(id+"-answer", "final_answer", text)}}
	}
	newestFirst := []any{
		turn("turn-3", "failed", "no end in the rollout"),
		turn("turn-2", "interrupted", "stopping"),
		turn("turn-1", "completed", "built"),
	}
	startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": "thread"}}
		case "thread/turns/list":
			return map[string]any{"data": newestFirst}
		}
		return nil
	})
	read, err := isolatedManagedRuntime(t).ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil || read.Source != "rpc" || len(read.Exchanges) != 3 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	wantTurn(t, "completed", read.Exchanges[0].Turn, codexCompletedTurn)
	wantTurn(t, "aborted", read.Exchanges[1].Turn, codexAbortedTurn)
	wantTurn(t, "app server only", read.Exchanges[2].Turn, &surface.TurnMetadata{V: 1, State: "failed", Unavailable: []string{"model", "durationMs", "usage"}})

	startManagedCodex(t, managedThreadReads(managedTurn("turn-1", "completed", agentMessage("final", "final_answer", "built"))))
	var done *surface.TurnMetadata
	err = isolatedManagedRuntime(t).Stream(context.Background(), session, "turn-1", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			done = event.Turn
		}
	}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	wantTurn(t, "streamed", done, codexCompletedTurn)
}
