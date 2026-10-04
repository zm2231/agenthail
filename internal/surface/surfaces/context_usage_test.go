package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeContextUsageTracksCompactionAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":129000,"output_tokens":500}}}`,
		`{"type":"user","timestamp":"2026-07-16T01:01:00Z","message":{"content":"/compact"}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-16T01:01:02Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
	)
	adapter := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "claude", Surface: surface.KindClaude, Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedTokens != 42000 || usage.ContextWindow != 0 || usage.WindowEstimated || usage.CompactionCount != 1 || usage.ReclaimedTokens != 108000 || usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
	appendTestTranscript(t, path, `{"type":"user","timestamp":"2026-07-16T01:02:00Z","message":{"content":"/compact"}}`)
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
	appendTestTranscript(t, path,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-16T01:02:02Z","compactMetadata":{"preTokens":151000,"postTokens":41000}}`,
		`{"type":"assistant","timestamp":"2026-07-16T01:02:04Z","message":{"model":"claude-opus-4-8[1m]","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":40000,"output_tokens":100}}}`,
	)
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Compacting || usage.UsedTokens != 41000 || usage.ContextWindow != 0 || usage.WindowEstimated || usage.CompactionCount != 2 {
		t.Fatalf("usage=%+v", usage)
	}
	appendTestTranscript(t, path, `{"type":"user","timestamp":"2026-07-16T01:03:00Z","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}`)
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
	appendTestTranscript(t, path, `{"type":"system","subtype":"local_command","timestamp":"2026-07-16T01:03:03Z","content":"done"}`)
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageReturnsTokensWhenWindowIsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":249000,"output_tokens":500}}}`,
	)
	adapter := NewClaude("", t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.UsedTokens != 250000 || usage.ContextWindow != 0 || usage.WindowEstimated {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageUsesExplicitConfiguredWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":249000,"output_tokens":500}}}`,
	)
	adapter := NewClaude("", t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, Transcript: path, ConfiguredModel: "claude-opus-5-5[1m]"})
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.ContextWindow != 1_000_000 || usage.ContextWindowSource != ClaudeContextWindowSourceConfigured || usage.WindowEstimated {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageInvalidatesStaleConfiguredWindowAfterObservedModelSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":249000,"output_tokens":500}}}`,
	)
	adapter := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "claude", Surface: surface.KindClaude, Transcript: path, ConfiguredModel: "claude-opus-5-5[1m]"}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil || usage.ContextWindow != 1_000_000 {
		t.Fatalf("initial usage=%+v err=%v", usage, err)
	}
	appendTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:01:00Z","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":1000,"output_tokens":500}}}`,
	)
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ContextWindow != 0 || usage.ContextWindowSource != ClaudeContextWindowSourceUnknown || usage.WindowEstimated {
		t.Fatalf("stale capacity=%+v", usage)
	}
}

func TestClaudeContextUsageIgnoresOlderCommandAfterBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":129000,"output_tokens":500}}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-16T01:01:02Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
		`{"type":"user","timestamp":"2026-07-16T01:01:00Z","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}`,
	)
	adapter := NewClaude("", t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageCompletesCommandMarkerAtBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":129000,"output_tokens":500}}}`,
		`{"type":"user","timestamp":"2026-07-16T01:01:00Z","message":{"content":"/compact"}}`,
		`{"type":"user","timestamp":"2026-07-16T01:01:00.001Z","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-16T01:01:02Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
	)
	adapter := NewClaude("", t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageDeduplicatesReplayedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-06-29T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":149000,"output_tokens":500}}}`,
		`{"uuid":"boundary-old","type":"system","subtype":"compact_boundary","timestamp":"2026-06-29T01:01:00Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
		`{"uuid":"boundary-new","type":"system","subtype":"compact_boundary","timestamp":"2026-06-30T01:01:00Z","compactMetadata":{"preTokens":151000,"postTokens":41000}}`,
		`{"type":"assistant","timestamp":"2026-07-05T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":173446,"output_tokens":500}}}`,
		`{"uuid":"boundary-old","type":"system","subtype":"compact_boundary","timestamp":"2026-06-29T01:01:00Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
		`{"type":"assistant","timestamp":"2026-05-11T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":47192,"output_tokens":500}}}`,
	)
	adapter := NewClaude("", t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CompactionCount != 2 || usage.UsedTokens != 174446 || usage.PreCompactTokens != 151000 || usage.PostCompactTokens != 41000 || !usage.LastCompactedAt.Equal(time.Date(2026, 6, 30, 1, 1, 0, 0, time.UTC)) {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestCodexContextUsageUsesLatestContextSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	writeTestTranscript(t, path,
		codexTokenRecord("2026-07-16T04:29:00Z", 180000, 900000),
		`{"timestamp":"2026-07-16T04:30:11Z","type":"compacted","payload":{"window_number":3,"window_id":"019f6930-7747-7c63-9442-3c742e0589f0"}}`,
		codexTokenRecord("2026-07-16T04:30:13Z", 80000, 980000),
	)
	adapter := NewCodex("")
	session := &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedTokens != 80000 || usage.ContextWindow != 258400 || usage.CumulativeTokens != 980000 || usage.CompactionCount != 3 || usage.PreCompactTokens != 180000 || usage.PostCompactTokens != 80000 || usage.ReclaimedTokens != 100000 || usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestCodexContextUsageResetsWhenTranscriptIsReplacedAtSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	writeTestTranscript(t, path, codexTokenRecord("2026-07-16T04:29:00Z", 100, 100))
	adapter := NewCodex("")
	session := &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil || usage == nil || usage.UsedTokens != 100 {
		t.Fatalf("initial usage=%+v err=%v", usage, err)
	}
	replacement := filepath.Join(t.TempDir(), "replacement.jsonl")
	writeTestTranscript(t, replacement, codexTokenRecord("2026-07-16T04:30:00Z", 200, 200))
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil || usage == nil || usage.UsedTokens != 200 {
		t.Fatalf("replacement usage=%+v err=%v", usage, err)
	}
}

func TestCodexContextUsageTracksRapidCompactions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	writeTestTranscript(t, path,
		codexTokenRecord("2026-07-16T04:29:00Z", 180000, 900000),
		`{"timestamp":"2026-07-16T04:30:00Z","type":"compacted","payload":{"window_number":1,"window_id":"019f6930-0000-7000-8000-000000000000"}}`,
		codexTokenRecord("2026-07-16T04:30:01Z", 80000, 980000),
		codexTokenRecord("2026-07-16T04:31:00Z", 100000, 1000000),
		`{"timestamp":"2026-07-16T04:32:00Z","type":"compacted","payload":{"window_number":2,"window_id":"019f6931-d4c0-7000-8000-000000000000"}}`,
		codexTokenRecord("2026-07-16T04:32:01Z", 40000, 1040000),
	)
	adapter := NewCodex("")
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CompactionCount != 2 || usage.PreCompactTokens != 100000 || usage.PostCompactTokens != 40000 || usage.ReclaimedTokens != 60000 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestCodexContextUsageIgnoresPartialRecordUntilComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	writeTestTranscript(t, path, codexTokenRecord("2026-07-16T04:29:00Z", 120000, 120000))
	partial := codexTokenRecord("2026-07-16T04:30:00Z", 130000, 250000)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(partial[:len(partial)/2]); err != nil {
		t.Fatal(err)
	}
	file.Close()
	adapter := NewCodex("")
	session := &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedTokens != 120000 {
		t.Fatalf("usage=%+v", usage)
	}
	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(partial[len(partial)/2:] + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedTokens != 130000 {
		t.Fatalf("usage=%+v", usage)
	}
}

// liveContext drives managed Streams for a session with a local transcript.
// Each Stream first sees its turn in progress, so the transcript poll sets the
// baseline, and then receives the queued notifications with the completed turn.
type liveContext struct {
	codex   *Codex
	session *surface.Session
	mu      sync.Mutex
	reads   int
	batch   []liveNotification
}

type liveNotification struct {
	method string
	params map[string]any
}

func newLiveContext(t *testing.T, records ...string) *liveContext {
	t.Helper()
	live := &liveContext{}
	var fake *managedCodex
	fake = startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method != "thread/read" {
			return nil
		}
		live.mu.Lock()
		defer live.mu.Unlock()
		live.reads++
		if live.reads%2 == 1 {
			return managedTurn("turn", "inProgress")
		}
		for _, notification := range live.batch {
			fake.Notify(notification.method, notification.params)
		}
		live.batch = nil
		return managedTurn("turn", "completed", agentMessage("final", "final_answer", "done"))
	})
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	writeTestTranscript(t, path, records...)
	live.codex = isolatedManagedRuntime(t)
	live.session = &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, Transport: codexTransportManaged, Transcript: path}
	return live
}

func (l *liveContext) compaction(phase string) liveNotification {
	return liveNotification{"item/" + phase, map[string]any{"threadId": l.session.ID, "item": map[string]any{"type": "contextCompaction"}}}
}

func (l *liveContext) tokenUsage(current, cumulative float64) liveNotification {
	return liveNotification{"thread/tokenUsage/updated", map[string]any{
		"threadId": l.session.ID,
		"tokenUsage": map[string]any{
			"last":               map[string]any{"inputTokens": current - 20, "totalTokens": current},
			"total":              map[string]any{"totalTokens": cumulative},
			"modelContextWindow": float64(258400),
		},
	}}
}

// stream delivers notifications in one Stream window and returns the last
// context usage it emitted.
func (l *liveContext) stream(t *testing.T, notifications ...liveNotification) *surface.ContextUsage {
	t.Helper()
	l.mu.Lock()
	l.batch = notifications
	l.mu.Unlock()
	var last *surface.ContextUsage
	if err := l.codex.Stream(context.Background(), l.session, "turn", func(event surface.StreamEvent) {
		if event.Kind == "context" {
			last = event.Context
		}
	}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if last == nil {
		t.Fatal("stream emitted no context usage")
	}
	return last
}

func TestCodexLiveContextEventsSurviveStaleTranscriptPolls(t *testing.T) {
	live := newLiveContext(t, codexTokenRecord("2026-07-16T04:29:00Z", 90000, 90000))
	if usage := live.stream(t, live.compaction("started"), live.tokenUsage(100000, 190000)); usage.UsedTokens != 100000 || !usage.Compacting {
		t.Fatalf("live=%+v", usage)
	}
	if usage, err := live.codex.ContextUsage(context.Background(), live.session); err != nil || usage.UsedTokens != 100000 || !usage.Compacting {
		t.Fatalf("poll after live events usage=%+v err=%v", usage, err)
	}
	if usage := live.stream(t, live.compaction("completed")); usage.Compacting {
		t.Fatalf("completed=%+v", usage)
	}
}

func TestCodexStaleLiveCompactionPreservesRecoveredSavings(t *testing.T) {
	live := newLiveContext(t,
		codexTokenRecord("2026-07-16T04:29:00Z", 180000, 900000),
		`{"timestamp":"2026-07-16T04:30:11Z","type":"compacted","payload":{"window_number":3,"window_id":"019f6930-7747-7c63-9442-3c742e0589f0"}}`,
		codexTokenRecord("2026-07-16T04:30:13Z", 80000, 980000),
	)
	if usage := live.stream(t, live.compaction("started"), live.tokenUsage(80000, 980000)); usage.PreCompactTokens != 180000 || usage.PostCompactTokens != 80000 || usage.ReclaimedTokens != 100000 {
		t.Fatalf("stale live usage=%+v", usage)
	}
	if usage := live.stream(t, live.compaction("completed")); usage.Compacting || usage.PreCompactTokens != 180000 || usage.PostCompactTokens != 80000 || usage.ReclaimedTokens != 100000 {
		t.Fatalf("completed=%+v", usage)
	}
}

func TestCodexReadSessionFindsTranscriptByLocalSessionDate(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("EDT", -4*60*60)
	t.Cleanup(func() { time.Local = originalLocal })
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	id := uuidV7ForTest(time.Date(2026, 6, 23, 1, 30, 0, 0, time.UTC))
	path := filepath.Join(home, "sessions", "2026", "06", "22", "rollout-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	writeTestTranscript(t, path, `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"found"}]}}`)
	read, err := (&Codex{}).ReadSession(context.Background(), &surface.Session{ID: id}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Items) != 1 || read.Items[0].Text != "found" {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	notV7 := "1845b4c6-eb75-4e59-b439-b95c3698ac31"
	read, err = (&Codex{}).ReadSession(context.Background(), &surface.Session{ID: notV7}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Items) != 0 || read.UnavailableReason == "" {
		t.Fatalf("UUIDv4 was treated as a transcript timestamp: read=%+v err=%v", read, err)
	}
}

func uuidV7ForTest(at time.Time) string {
	prefix := fmt.Sprintf("%012x", at.UnixMilli())
	return prefix[:8] + "-" + prefix[8:] + "-7000-8000-000000000000"
}

func codexTokenRecord(timestamp string, current, cumulative int64) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":%d},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":%d},"model_context_window":258400}}}`, timestamp, cumulative-20, cumulative, current-20, current)
}

func writeTestTranscript(t *testing.T, path string, records ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(stringsJoinLines(records)), 0600); err != nil {
		t.Fatal(err)
	}
}

func appendTestTranscript(t *testing.T, path string, records ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(stringsJoinLines(records)); err != nil {
		t.Fatal(err)
	}
}

func stringsJoinLines(records []string) string {
	result := ""
	for _, record := range records {
		result += record + "\n"
	}
	return result
}
