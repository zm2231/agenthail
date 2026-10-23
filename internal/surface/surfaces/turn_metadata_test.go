package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
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

const codexTurnRecords = `{"timestamp":"2026-10-19T10:00:00Z","type":"session_meta","payload":{"id":"thread"}}
{"timestamp":"2026-10-19T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
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
{"timestamp":"2026-10-19T10:01:01Z","type":"compacted","payload":{"message":"","window_number":1,"window_id":"window-2"}}
{"timestamp":"2026-10-19T10:01:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":300},"last_token_usage":{"input_tokens":150,"cached_input_tokens":100,"cache_write_input_tokens":0,"output_tokens":30,"reasoning_output_tokens":2,"total_tokens":180}}}}
{"timestamp":"2026-10-19T10:01:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":330},"last_token_usage":{"input_tokens":30,"total_tokens":30}}}}
{"timestamp":"2026-10-19T10:01:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":380},"last_token_usage":{"input_tokens":40,"output_tokens":10,"total_tokens":50}}}}
{"timestamp":"2026-10-19T10:01:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"stopping"}]}}
{"timestamp":"2026-10-19T10:01:03Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-2","parent_turn_id":"turn-1"}}
`

var (
	codexCompletedTurn = &surface.TurnMetadata{V: 1, State: "completed", Model: "gpt-fixture", DurationMs: count(4200), Usage: &surface.TurnUsage{InputTokens: 250, CachedInputTokens: 140, CacheWriteTokens: 5, OutputTokens: 50, ReasoningTokens: count(10)}, ContextTokens: count(180)}
	codexAbortedTurn   = &surface.TurnMetadata{V: 1, State: "interrupted", Model: "gpt-fixture", Usage: &surface.TurnUsage{InputTokens: 70, OutputTokens: 10}, ContextTokens: count(50),
		Compactions: []surface.TurnCompaction{{Trigger: "unknown", PreTokens: count(180), PostTokens: count(30)}}, Unavailable: []string{"durationMs", "usage.reasoningTokens"}}
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
	for _, start := range []string{"task_started", "turn_started"} {
		t.Run(start, func(t *testing.T) {
			head := strings.ReplaceAll(codexTurnRecords, `"task_started"`, `"`+start+`"`)
			path := timelineFixture(t, head)
			offset := int64(len(head))
			testkit.AppendFile(t, path, strings.ReplaceAll(codexTurnRest, `"task_started"`, `"`+start+`"`))
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
		})
	}
}

func TestCodexTurnWhoseStartIsOutOfReachHasNoUsage(t *testing.T) {
	limit := turnScanLimit
	turnScanLimit = 1
	t.Cleanup(func() { turnScanLimit = limit })
	path := timelineFixture(t, codexTurnRecords)
	testkit.AppendFile(t, path, codexTurnRest)
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
	wantTurn(t, "joined midway", first, &surface.TurnMetadata{V: 1, State: "completed", DurationMs: count(4200), Unavailable: []string{"model", "usage", "contextTokens", "compactions"}})
}

const claudeTurnHead = `{"type":"permission-mode","permissionMode":"default","sessionId":"session"}
{"type":"user","uuid":"u1","timestamp":"2026-10-19T10:00:00Z","message":{"content":"fix it"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-19T10:00:02Z","message":{"id":"m1","model":"claude-fixture","content":[{"type":"thinking","thinking":"","signature":"sig"}],"usage":{"input_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-19T10:00:03Z","message":{"id":"m1","model":"claude-fixture","content":[{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"go test"}}],"usage":{"input_tokens":3,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000,"output_tokens":40,"output_tokens_details":{"thinking_tokens":12}}}}
`

const claudeTurnTail = `{"type":"system","subtype":"compact_boundary","uuid":"cb1","timestamp":"2026-10-19T10:00:03.5Z","content":"Conversation compacted","compactMetadata":{"trigger":"auto","preTokens":1103,"postTokens":200}}
{"type":"user","uuid":"summary","isCompactSummary":true,"timestamp":"2026-10-19T10:00:03.6Z","message":{"content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation."}}
{"type":"user","uuid":"r1","timestamp":"2026-10-19T10:00:04Z","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]}}
{"type":"assistant","uuid":"a3","timestamp":"2026-10-19T10:00:07Z","message":{"id":"m2","model":"claude-fixture","stop_reason":"end_turn","content":[{"type":"text","text":"fixed"}],"usage":{"input_tokens":5,"cache_creation_input_tokens":20,"cache_read_input_tokens":1100,"output_tokens":15,"output_tokens_details":{"thinking_tokens":0}}}}
{"type":"system","subtype":"turn_duration","uuid":"s1","timestamp":"2026-10-19T10:00:07.5Z"}
{"type":"system","subtype":"compact_boundary","uuid":"cb2","timestamp":"2026-10-19T10:00:40Z","content":"Conversation compacted","compactMetadata":{"trigger":"auto","preTokens":1300,"postTokens":250}}
{"type":"user","uuid":"summary2","isCompactSummary":true,"timestamp":"2026-10-19T10:00:40.1Z","message":{"content":"This session is being continued from a previous conversation that ran out of context."}}
{"type":"system","subtype":"informational","uuid":"n1","timestamp":"2026-10-19T10:00:40.2Z","level":"notice","content":"Compacted while idle, before the prompt cache expired · turn off with \"idleCompaction\": false in settings.json"}
{"type":"user","uuid":"c1","timestamp":"2026-10-19T10:00:50Z","message":{"content":"<command-name>/compact</command-name>\n<command-message>compact</command-message>\n<command-args></command-args>"}}
{"type":"system","subtype":"compact_boundary","uuid":"cb3","timestamp":"2026-10-19T10:00:55Z","content":"Conversation compacted","compactMetadata":{"trigger":"manual","preTokens":260,"postTokens":240}}
{"type":"user","uuid":"u2","timestamp":"2026-10-19T10:01:00Z","message":{"content":"and the docs"}}
{"type":"assistant","uuid":"a4","timestamp":"2026-10-19T10:01:02Z","message":{"id":"m3","model":"claude-fixture","content":[{"type":"text","text":"writing"}],"usage":{"input_tokens":7,"cache_creation_input_tokens":0,"cache_read_input_tokens":50,"output_tokens":9}}}
{"type":"assistant","uuid":"a5","timestamp":"2026-10-19T10:01:04Z","message":{"id":"m4","model":"<synthetic>","content":[{"type":"text","text":"API Error: overloaded"}]}}
{"type":"system","subtype":"compact_boundary","uuid":"cb4","timestamp":"2026-10-19T10:01:04.5Z","content":"Conversation compacted","compactMetadata":{"trigger":"auto","preTokens":57,"postTokens":30}}
{"type":"assistant","uuid":"a6","timestamp":"2026-10-19T10:01:04.6Z","message":{"id":"m5","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}],"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}
{"type":"user","uuid":"interrupt","timestamp":"2026-10-19T10:01:05Z","message":{"content":"[Request interrupted by user]"}}
`

var (
	claudeCompletedTurn = &surface.TurnMetadata{V: 1, State: "completed", Model: "claude-fixture", DurationMs: count(7000), Usage: &surface.TurnUsage{InputTokens: 2228, CachedInputTokens: 2100, CacheWriteTokens: 120, OutputTokens: 55, ReasoningTokens: count(12)}, ContextTokens: count(1125),
		Compactions: []surface.TurnCompaction{{Trigger: "threshold", PreTokens: count(1103), PostTokens: count(200)}}}
	claudeInterruptedTurn = &surface.TurnMetadata{V: 1, State: "interrupted", Model: "claude-fixture", DurationMs: count(5000), Usage: &surface.TurnUsage{InputTokens: 57, CachedInputTokens: 50, OutputTokens: 9}, ContextTokens: count(30),
		Compactions: []surface.TurnCompaction{{Trigger: "idle", PreTokens: count(1300), PostTokens: count(250)}, {Trigger: "manual", PreTokens: count(260), PostTokens: count(240)}, {Trigger: "threshold", PreTokens: count(57), PostTokens: count(30)}}, Unavailable: []string{"usage.reasoningTokens"}}
)

func TestClaudeTranscriptReportsEachTurnsModelUsageAndDuration(t *testing.T) {
	for _, notice := range []string{"Compacted while idle, before the prompt cache expired", "Compacted after an hour idle, using a summary written while you were away"} {
		path := timelineFixture(t, claudeTurnHead+strings.Replace(claudeTurnTail, "Compacted while idle, before the prompt cache expired", notice, 1))
		read, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
		if err != nil || len(read.Exchanges) != 2 {
			t.Fatalf("read=%+v err=%v", read, err)
		}
		wantTurn(t, "completed", read.Exchanges[0].Turn, claudeCompletedTurn)
		wantTurn(t, notice, read.Exchanges[1].Turn, claudeInterruptedTurn)
	}
}

func TestClaudeStreamResumedMidTurnCountsTheWholeTurn(t *testing.T) {
	path := writeTranscript(t, claudeTurnHead)
	session := seededClaudeStreamSession(t, path, "")
	testkit.AppendFile(t, path, claudeTurnTail)
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
	wantTurn(t, "in progress", read.Exchanges[0].Turn, &surface.TurnMetadata{V: 1, State: "running", Unavailable: []string{"model", "durationMs", "usage", "contextTokens", "compactions"}})

	path = timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
{"timestamp":"2026-09-07T12:02:00Z","type":"event_msg","payload":{"type":"user_message","message":"again"}}
`)
	read, err = (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	unknown := &surface.TurnMetadata{V: 1, State: "unknown", Unavailable: []string{"state", "model", "durationMs", "usage", "contextTokens", "compactions"}}
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
	rollout := strings.ReplaceAll(codexTurnRecords+strings.ReplaceAll(codexTurnRest, `"text":"stopping"`, `"text":"built"`), `"task_started"`, `"turn_started"`)
	path := timelineFixture(t, rollout+`{"timestamp":"2026-10-19T10:02:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-3"}}
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
		turn("turn-2", "interrupted", "built"),
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
	wantTurn(t, "app server only", read.Exchanges[2].Turn, &surface.TurnMetadata{V: 1, State: "failed", Unavailable: []string{"model", "durationMs", "usage", "contextTokens", "compactions"}})

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

// streamedClaudeTurns streams rest after head until the last turn ends, and
// returns every turn that ended on the way.
func streamedClaudeTurns(t *testing.T, head, rest, last string) map[string]*surface.TurnMetadata {
	t.Helper()
	path := writeTranscript(t, head)
	session := seededClaudeStreamSession(t, path, "")
	testkit.AppendFile(t, path, rest)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	turns := map[string]*surface.TurnMetadata{}
	err := NewClaude(nil, t.TempDir()).Stream(ctx, session, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			turns[event.TurnID] = event.Turn
			if event.TurnID == last {
				cancel()
			}
		}
	}, 5*time.Second)
	if _, ended := turns[last]; !ended || !errors.Is(err, context.Canceled) {
		t.Fatalf("turn %s ended=%v err=%v", last, ended, err)
	}
	return turns
}

func TestClaudeCompactionsBetweenTurnsBelongToTheNextTurnWhereverAReaderJoins(t *testing.T) {
	split := strings.Index(claudeTurnTail, `{"type":"user","uuid":"u2"`)
	turns := streamedClaudeTurns(t, claudeTurnHead+claudeTurnTail[:split], claudeTurnTail[split:], "u2")
	if len(turns) != 1 {
		t.Fatalf("turns=%v", turns)
	}
	wantTurn(t, "after the idle compaction", turns["u2"], claudeInterruptedTurn)
}

func TestClaudeCompactionInATurnJoinedMidwayIsNotTheNextTurns(t *testing.T) {
	limit := turnScanLimit
	turnScanLimit = 1
	t.Cleanup(func() { turnScanLimit = limit })
	turns := streamedClaudeTurns(t, claudeTurnHead, claudeTurnTail, "u2")
	wantTurn(t, "after a turn joined midway", turns["u2"], claudeInterruptedTurn)
}

func TestCodexCompactionInATurnJoinedMidwayIsNotTheNextTurns(t *testing.T) {
	limit := turnScanLimit
	turnScanLimit = 1
	t.Cleanup(func() { turnScanLimit = limit })
	path := timelineFixture(t, codexTurnRecords)
	testkit.AppendFile(t, path, `{"timestamp":"2026-10-19T10:00:02Z","type":"compacted","payload":{"message":"","window_number":1,"window_id":"window-1"}}`+"\n"+codexTurnRest)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var turns []*surface.TurnMetadata
	err := (&Codex{}).Stream(ctx, &surface.Session{ID: "thread", Transcript: path, TranscriptOffset: int64(len(codexTurnRecords)), TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			if turns = append(turns, event.Turn); len(turns) == 2 {
				cancel()
			}
		}
	}, 2*time.Second)
	if err != nil && !errors.Is(err, context.Canceled) || len(turns) != 2 {
		t.Fatalf("turns=%d err=%v", len(turns), err)
	}
	wantTurn(t, "after a turn joined midway", turns[1], codexAbortedTurn)
}

func streamedCodexTurns(t *testing.T, head, rest string, want int) []*surface.TurnMetadata {
	t.Helper()
	path := timelineFixture(t, head)
	testkit.AppendFile(t, path, rest)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var turns []*surface.TurnMetadata
	err := (&Codex{}).Stream(ctx, &surface.Session{ID: "thread", Transcript: path, TranscriptOffset: int64(len(head)), TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			if turns = append(turns, event.Turn); len(turns) == want {
				cancel()
			}
		}
	}, 2*time.Second)
	if err != nil && !errors.Is(err, context.Canceled) || len(turns) != want {
		t.Fatalf("turns=%d err=%v", len(turns), err)
	}
	return turns
}

func TestAReaderJoiningAfterACompactionReportsTheTurnsWholeCompactionHistory(t *testing.T) {
	claudeSplit := strings.Index(claudeTurnTail, `{"type":"assistant","uuid":"a5"`)
	wantTurn(t, "claude", streamedClaudeTurns(t, claudeTurnHead+claudeTurnTail[:claudeSplit], claudeTurnTail[claudeSplit:], "u2")["u2"], claudeInterruptedTurn)
	records := codexTurnRecords + codexTurnRest
	codexSplit := strings.Index(records, `{"timestamp":"2026-10-19T10:01:02Z","type":"event_msg","payload":{"type":"token_count"`)
	wantTurn(t, "codex", streamedCodexTurns(t, records[:codexSplit], records[codexSplit:], 1)[0], codexAbortedTurn)
}

func TestATurnWhoseEarlierHistoryIsOutOfReachDoesNotClaimItHadNoCompactions(t *testing.T) {
	records := codexTurnRecords + codexTurnRest
	start := strings.Index(records, `{"timestamp":"2026-10-19T10:01:00Z","type":"event_msg","payload":{"type":"task_started"`)
	split := strings.Index(records, `{"timestamp":"2026-10-19T10:01:02Z","type":"event_msg","payload":{"type":"token_count"`)
	limit := turnScanLimit
	turnScanLimit = int64(split - start + 1)
	t.Cleanup(func() { turnScanLimit = limit })
	turn := streamedCodexTurns(t, records[:split], records[split:], 1)[0]
	if turn.Compactions != nil || !slices.Contains(turn.Unavailable, "compactions") {
		t.Fatalf("codex turn=%s, want its compactions unavailable", turnJSON(t, turn))
	}

	claude := claudeTurnHead + claudeTurnTail
	start = strings.Index(claude, `{"type":"user","uuid":"u2"`)
	split = strings.Index(claude, `{"type":"assistant","uuid":"a5"`)
	turnScanLimit = int64(split - start + 1)
	turn = streamedClaudeTurns(t, claude[:split], claude[split:], "u2")["u2"]
	if turn == nil || turn.Compactions != nil || !slices.Contains(turn.Unavailable, "compactions") {
		t.Fatalf("claude turn=%s, want its compactions unavailable", turnJSON(t, turn))
	}
}

func TestCodexCompactionsBackToBackLeaveTheSizeBetweenThemUnknown(t *testing.T) {
	for _, between := range []struct {
		name    string
		records string
		pre     *int64
	}{
		{name: "nothing measured"},
		{name: "a request no smaller", records: `{"timestamp":"2026-10-19T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":490,"output_tokens":10,"total_tokens":500}}}}
`, pre: count(500)},
	} {
		t.Run(between.name, func(t *testing.T) {
			path := timelineFixture(t, `{"timestamp":"2026-10-19T10:00:00Z","type":"session_meta","payload":{"id":"thread"}}
{"timestamp":"2026-10-19T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-10-19T10:00:00Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-fixture"}}
{"timestamp":"2026-10-19T10:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"build it"}}
{"timestamp":"2026-10-19T10:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":500},"last_token_usage":{"input_tokens":480,"output_tokens":20,"total_tokens":500}}}}
{"timestamp":"2026-10-19T10:00:02Z","type":"compacted","payload":{"message":"","window_number":1,"window_id":"window-1"}}
`+between.records+`{"timestamp":"2026-10-19T10:00:03Z","type":"compacted","payload":{"message":"","window_number":2,"window_id":"window-2"}}
{"timestamp":"2026-10-19T10:00:04Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1040},"last_token_usage":{"input_tokens":40,"total_tokens":40}}}}
{"timestamp":"2026-10-19T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"built"}]}}
{"timestamp":"2026-10-19T10:00:05Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","duration_ms":5000}}
`)
			read, err := (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
			if err != nil || len(read.Exchanges) != 1 {
				t.Fatalf("read=%+v err=%v", read, err)
			}
			turn := read.Exchanges[0].Turn
			want := []surface.TurnCompaction{{Trigger: "unknown", PreTokens: count(500)}, {Trigger: "unknown", PreTokens: between.pre, PostTokens: count(40)}}
			if !reflect.DeepEqual(turn.Compactions, want) || *turn.ContextTokens != 40 {
				t.Fatalf("turn=%s", turnJSON(t, turn))
			}
		})
	}
}
