package surfaces

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
	"github.com/zm2231/agenthail/internal/testkit"
)

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
	claudeCompletedTurn = &surface.TurnMetadata{V: 1, State: "completed", Model: "claude-fixture", DurationMs: surfacetest.Count(7000), Usage: &surface.TurnUsage{InputTokens: 2228, CachedInputTokens: 2100, CacheWriteTokens: 120, OutputTokens: 55, ReasoningTokens: surfacetest.Count(12)}, ContextTokens: surfacetest.Count(1125),
		Compactions: []surface.TurnCompaction{{Trigger: "threshold", PreTokens: surfacetest.Count(1103), PostTokens: surfacetest.Count(200)}}}
	claudeInterruptedTurn = &surface.TurnMetadata{V: 1, State: "interrupted", Model: "claude-fixture", DurationMs: surfacetest.Count(5000), Usage: &surface.TurnUsage{InputTokens: 57, CachedInputTokens: 50, OutputTokens: 9}, ContextTokens: surfacetest.Count(30),
		Compactions: []surface.TurnCompaction{{Trigger: "idle", PreTokens: surfacetest.Count(1300), PostTokens: surfacetest.Count(250)}, {Trigger: "manual", PreTokens: surfacetest.Count(260), PostTokens: surfacetest.Count(240), RequestID: "c1"}, {Trigger: "threshold", PreTokens: surfacetest.Count(57), PostTokens: surfacetest.Count(30)}}, Unavailable: []string{"usage.reasoningTokens"}}
)

func TestClaudeTranscriptReportsEachTurnsModelUsageAndDuration(t *testing.T) {
	for _, notice := range []string{"Compacted while idle, before the prompt cache expired", "Compacted after an hour idle, using a summary written while you were away"} {
		path := surfacetest.TimelineFixture(t, claudeTurnHead+strings.Replace(claudeTurnTail, "Compacted while idle, before the prompt cache expired", notice, 1))
		read, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
		if err != nil || len(read.Exchanges) != 2 {
			t.Fatalf("read=%+v err=%v", read, err)
		}
		surfacetest.WantTurn(t, "completed", read.Exchanges[0].Turn, claudeCompletedTurn)
		surfacetest.WantTurn(t, notice, read.Exchanges[1].Turn, claudeInterruptedTurn)
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
	surfacetest.WantTurn(t, "completed", turns["u1"], claudeCompletedTurn)
	surfacetest.WantTurn(t, "interrupted", turns["u2"], claudeInterruptedTurn)
}

func TestAClaudeTurnStillInProgressIsRunning(t *testing.T) {
	path := surfacetest.TimelineFixture(t, claudeTurnHead)
	read, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusBusy}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 1 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	surfacetest.WantTurn(t, "in progress", read.Exchanges[0].Turn, &surface.TurnMetadata{V: 1, State: "running", Unavailable: []string{"model", "durationMs", "usage", "contextTokens", "compactions"}})
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
	surfacetest.WantTurn(t, "after the idle compaction", turns["u2"], claudeInterruptedTurn)
}

func TestClaudeCompactionInATurnJoinedMidwayIsNotTheNextTurns(t *testing.T) {
	limit := transcript.TurnScanLimit
	transcript.TurnScanLimit = 1
	t.Cleanup(func() { transcript.TurnScanLimit = limit })
	turns := streamedClaudeTurns(t, claudeTurnHead, claudeTurnTail, "u2")
	surfacetest.WantTurn(t, "after a turn joined midway", turns["u2"], claudeInterruptedTurn)
}

func TestAReaderJoiningAfterACompactionReportsTheTurnsWholeCompactionHistory(t *testing.T) {
	claudeSplit := strings.Index(claudeTurnTail, `{"type":"assistant","uuid":"a5"`)
	surfacetest.WantTurn(t, "claude", streamedClaudeTurns(t, claudeTurnHead+claudeTurnTail[:claudeSplit], claudeTurnTail[claudeSplit:], "u2")["u2"], claudeInterruptedTurn)
}

func TestATurnWhoseEarlierHistoryIsOutOfReachDoesNotClaimItHadNoCompactions(t *testing.T) {
	claude := claudeTurnHead + claudeTurnTail
	start := strings.Index(claude, `{"type":"user","uuid":"u2"`)
	split := strings.Index(claude, `{"type":"assistant","uuid":"a5"`)
	limit := transcript.TurnScanLimit
	transcript.TurnScanLimit = int64(split - start + 1)
	t.Cleanup(func() { transcript.TurnScanLimit = limit })
	turn := streamedClaudeTurns(t, claude[:split], claude[split:], "u2")["u2"]
	if turn == nil || turn.Compactions != nil || !slices.Contains(turn.Unavailable, "compactions") {
		t.Fatalf("claude turn=%s, want its compactions unavailable", surfacetest.TurnJSON(t, turn))
	}
}
