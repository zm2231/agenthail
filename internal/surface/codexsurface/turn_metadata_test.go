package codexsurface

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestCodexCompactionInATurnJoinedMidwayIsNotTheNextTurns(t *testing.T) {
	limit := transcript.TurnScanLimit
	transcript.TurnScanLimit = 1
	t.Cleanup(func() { transcript.TurnScanLimit = limit })
	path := surfacetest.TimelineFixture(t, codexTurnRecords)
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
	surfacetest.WantTurn(t, "after a turn joined midway", turns[1], codexAbortedTurn)
}

func TestCodexCompactionsBackToBackLeaveTheSizeBetweenThemUnknown(t *testing.T) {
	for _, between := range []struct {
		name    string
		records string
		pre     *int64
	}{
		{name: "nothing measured"},
		{name: "a request no smaller", records: `{"timestamp":"2026-10-19T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1000},"last_token_usage":{"input_tokens":490,"output_tokens":10,"total_tokens":500}}}}
`, pre: surfacetest.Count(500)},
	} {
		t.Run(between.name, func(t *testing.T) {
			path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-10-19T10:00:00Z","type":"session_meta","payload":{"id":"thread"}}
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
			want := []surface.TurnCompaction{{Trigger: "unknown", PreTokens: surfacetest.Count(500)}, {Trigger: "unknown", PreTokens: between.pre, PostTokens: surfacetest.Count(40)}}
			if !reflect.DeepEqual(turn.Compactions, want) || *turn.ContextTokens != 40 {
				t.Fatalf("turn=%s", surfacetest.TurnJSON(t, turn))
			}
		})
	}
}

func TestCodexStreamResumedMidTurnCountsTheWholeTurn(t *testing.T) {
	for _, start := range []string{"task_started", "turn_started"} {
		t.Run(start, func(t *testing.T) {
			head := strings.ReplaceAll(codexTurnRecords, `"task_started"`, `"`+start+`"`)
			path := surfacetest.TimelineFixture(t, head)
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
			surfacetest.WantTurn(t, "completed", turns[0], codexCompletedTurn)
			surfacetest.WantTurn(t, "aborted", turns[1], codexAbortedTurn)
		})
	}
}

func TestCodexTranscriptReportsEachTurnsModelUsageAndDuration(t *testing.T) {
	path := surfacetest.TimelineFixture(t, codexTurnRecords+codexTurnRest)
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	read, err := codex.ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	surfacetest.WantTurn(t, "completed", read.Exchanges[0].Turn, codexCompletedTurn)
	surfacetest.WantTurn(t, "aborted", read.Exchanges[1].Turn, codexAbortedTurn)
}

func TestCodexTurnWhoseStartIsOutOfReachHasNoUsage(t *testing.T) {
	limit := transcript.TurnScanLimit
	transcript.TurnScanLimit = 1
	t.Cleanup(func() { transcript.TurnScanLimit = limit })
	path := surfacetest.TimelineFixture(t, codexTurnRecords)
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
	surfacetest.WantTurn(t, "joined midway", first, &surface.TurnMetadata{V: 1, State: "completed", DurationMs: surfacetest.Count(4200), Unavailable: []string{"model", "usage", "contextTokens", "compactions"}})
}

func TestManagedCodexTurnsCarryWhatTheirRolloutRecorded(t *testing.T) {
	rollout := strings.ReplaceAll(codexTurnRecords+strings.ReplaceAll(codexTurnRest, `"text":"stopping"`, `"text":"built"`), `"task_started"`, `"turn_started"`)
	path := surfacetest.TimelineFixture(t, rollout+`{"timestamp":"2026-10-19T10:02:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-3"}}
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
	surfacetest.WantTurn(t, "completed", read.Exchanges[0].Turn, codexCompletedTurn)
	surfacetest.WantTurn(t, "aborted", read.Exchanges[1].Turn, codexAbortedTurn)
	surfacetest.WantTurn(t, "app server only", read.Exchanges[2].Turn, &surface.TurnMetadata{V: 1, State: "failed", Unavailable: []string{"model", "durationMs", "usage", "contextTokens", "compactions"}})

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
	surfacetest.WantTurn(t, "streamed", done, codexCompletedTurn)
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

func streamedCodexTurns(t *testing.T, head, rest string, want int) []*surface.TurnMetadata {
	t.Helper()
	path := surfacetest.TimelineFixture(t, head)
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

func TestACodexReaderJoiningAfterACompactionReportsTheTurnsWholeCompactionHistory(t *testing.T) {
	records := codexTurnRecords + codexTurnRest
	codexSplit := strings.Index(records, `{"timestamp":"2026-10-19T10:01:02Z","type":"event_msg","payload":{"type":"token_count"`)
	surfacetest.WantTurn(t, "codex", streamedCodexTurns(t, records[:codexSplit], records[codexSplit:], 1)[0], codexAbortedTurn)
}

func TestACodexTurnWithoutAnEndIsUnknownUntilTheNextStarts(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
{"timestamp":"2026-09-07T12:02:00Z","type":"event_msg","payload":{"type":"user_message","message":"again"}}
`)
	read, err := (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	unknown := &surface.TurnMetadata{V: 1, State: "unknown", Unavailable: []string{"state", "model", "durationMs", "usage", "contextTokens", "compactions"}}
	surfacetest.WantTurn(t, "superseded", read.Exchanges[0].Turn, unknown)
	surfacetest.WantTurn(t, "idle tail", read.Exchanges[1].Turn, unknown)
	if strings.Contains(surfacetest.TurnJSON(t, unknown), "inputTokens") {
		t.Fatal("unavailable usage must not read as zero tokens")
	}

	path = surfacetest.TimelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-a"}}
{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
{"timestamp":"2026-09-07T12:02:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-b"}}
{"timestamp":"2026-09-07T12:02:05Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-b","duration_ms":5000}}
`)
	read, err = (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 1 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	surfacetest.WantTurn(t, "turn without an end before a silent turn", read.Exchanges[0].Turn, &surface.TurnMetadata{V: 1, State: "interrupted", Usage: &surface.TurnUsage{}, Unavailable: []string{"model", "durationMs", "usage.reasoningTokens", "contextTokens"}})
}

func TestACodexTurnWhoseEarlierHistoryIsOutOfReachDoesNotClaimItHadNoCompactions(t *testing.T) {
	records := codexTurnRecords + codexTurnRest
	start := strings.Index(records, `{"timestamp":"2026-10-19T10:01:00Z","type":"event_msg","payload":{"type":"task_started"`)
	split := strings.Index(records, `{"timestamp":"2026-10-19T10:01:02Z","type":"event_msg","payload":{"type":"token_count"`)
	limit := transcript.TurnScanLimit
	transcript.TurnScanLimit = int64(split - start + 1)
	t.Cleanup(func() { transcript.TurnScanLimit = limit })
	turn := streamedCodexTurns(t, records[:split], records[split:], 1)[0]
	if turn.Compactions != nil || !slices.Contains(turn.Unavailable, "compactions") {
		t.Fatalf("codex turn=%s, want its compactions unavailable", surfacetest.TurnJSON(t, turn))
	}
}

var (
	codexCompletedTurn = &surface.TurnMetadata{V: 1, State: "completed", Model: "gpt-fixture", DurationMs: surfacetest.Count(4200), Usage: &surface.TurnUsage{InputTokens: 250, CachedInputTokens: 140, CacheWriteTokens: 5, OutputTokens: 50, ReasoningTokens: surfacetest.Count(10)}, ContextTokens: surfacetest.Count(180)}
	codexAbortedTurn   = &surface.TurnMetadata{V: 1, State: "interrupted", Model: "gpt-fixture", Usage: &surface.TurnUsage{InputTokens: 70, OutputTokens: 10}, ContextTokens: surfacetest.Count(50),
		Compactions: []surface.TurnCompaction{{Trigger: "unknown", PreTokens: surfacetest.Count(180), PostTokens: surfacetest.Count(30)}}, Unavailable: []string{"durationMs", "usage.reasoningTokens"}}
)
