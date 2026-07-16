package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	if usage.UsedTokens != 42000 || usage.ContextWindow != 200000 || usage.CompactionCount != 1 || usage.ReclaimedTokens != 108000 || usage.Compacting {
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
	if usage.Compacting || usage.UsedTokens != 41000 || usage.ContextWindow != 1000000 || usage.CompactionCount != 2 {
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

func TestCodexContextEventsExposeLiveUsageAndCompaction(t *testing.T) {
	event := codexEvent{Method: "thread/tokenUsage/updated", Params: map[string]any{
		"threadId": "thread",
		"tokenUsage": map[string]any{
			"last":               map[string]any{"inputTokens": float64(90000), "cachedInputTokens": float64(80000), "outputTokens": float64(1000), "reasoningOutputTokens": float64(200), "totalTokens": float64(91000)},
			"total":              map[string]any{"totalTokens": float64(1000000)},
			"modelContextWindow": float64(258400),
		},
	}}
	usage, ok := codexContextEvent(event, surface.ContextUsage{CompactionCount: 4})
	if !ok || usage.UsedTokens != 91000 || usage.ContextWindow != 258400 || usage.CumulativeTokens != 1000000 || usage.CompactionCount != 4 {
		t.Fatalf("usage=%+v ok=%v", usage, ok)
	}
	started, ok := codexContextEvent(codexEvent{Method: "item/started", Params: map[string]any{"threadId": "thread", "item": map[string]any{"type": "contextCompaction"}}}, *usage)
	if !ok || !started.Compacting {
		t.Fatalf("started=%+v ok=%v", started, ok)
	}
	completed, ok := codexContextEvent(codexEvent{Method: "item/completed", Params: map[string]any{"threadId": "thread", "item": map[string]any{"type": "contextCompaction"}}}, *started)
	if !ok || completed.Compacting {
		t.Fatalf("completed=%+v ok=%v", completed, ok)
	}
}

func TestCodexLiveContextEventsSurviveStaleTranscriptPolls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	writeTestTranscript(t, path, codexTokenRecord("2026-07-16T04:29:00Z", 90000, 90000))
	adapter := NewCodex("")
	session := &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	started, ok := adapter.applyContextEvent(session, codexEvent{Method: "item/started", Params: map[string]any{"threadId": session.ID, "item": map[string]any{"type": "contextCompaction"}}}, *usage)
	if !ok || !started.Compacting {
		t.Fatalf("started=%+v ok=%v", started, ok)
	}
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil || !usage.Compacting {
		t.Fatalf("poll usage=%+v err=%v", usage, err)
	}
	live := codexEvent{Method: "thread/tokenUsage/updated", Params: map[string]any{
		"threadId": session.ID,
		"tokenUsage": map[string]any{
			"last":               map[string]any{"inputTokens": float64(99000), "totalTokens": float64(100000)},
			"total":              map[string]any{"totalTokens": float64(190000)},
			"modelContextWindow": float64(258400),
		},
	}}
	updated, ok := adapter.applyContextEvent(session, live, *usage)
	if !ok || updated.UsedTokens != 100000 || !updated.Compacting {
		t.Fatalf("updated=%+v ok=%v", updated, ok)
	}
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil || usage.UsedTokens != 100000 || !usage.Compacting {
		t.Fatalf("poll usage=%+v err=%v", usage, err)
	}
	completed, ok := adapter.applyContextEvent(session, codexEvent{Method: "item/completed", Params: map[string]any{"threadId": session.ID, "item": map[string]any{"type": "contextCompaction"}}}, *usage)
	if !ok || completed.Compacting {
		t.Fatalf("completed=%+v ok=%v", completed, ok)
	}
}

func TestCodexStaleLiveCompactionPreservesRecoveredSavings(t *testing.T) {
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
	started, ok := adapter.applyContextEvent(session, codexEvent{Method: "item/started", Params: map[string]any{"threadId": session.ID, "item": map[string]any{"type": "contextCompaction"}}}, *usage)
	if !ok || !started.Compacting {
		t.Fatalf("started=%+v ok=%v", started, ok)
	}
	stale := codexEvent{Method: "thread/tokenUsage/updated", Params: map[string]any{
		"threadId": session.ID,
		"tokenUsage": map[string]any{
			"last":               map[string]any{"inputTokens": float64(79980), "totalTokens": float64(80000)},
			"total":              map[string]any{"totalTokens": float64(980000)},
			"modelContextWindow": float64(258400),
		},
	}}
	updated, ok := adapter.applyContextEvent(session, stale, *started)
	if !ok || updated.PreCompactTokens != 180000 || updated.PostCompactTokens != 80000 || updated.ReclaimedTokens != 100000 {
		t.Fatalf("updated=%+v ok=%v", updated, ok)
	}
	completed, ok := adapter.applyContextEvent(session, codexEvent{Method: "item/completed", Params: map[string]any{"threadId": session.ID, "item": map[string]any{"type": "contextCompaction"}}}, *updated)
	if !ok || completed.Compacting || completed.PreCompactTokens != 180000 || completed.PostCompactTokens != 80000 || completed.ReclaimedTokens != 100000 {
		t.Fatalf("completed=%+v ok=%v", completed, ok)
	}
}

func TestUUIDV7TimeRejectsOtherUUIDVersions(t *testing.T) {
	if !uuidV7Time("1845b4c6-eb75-4e59-b439-b95c3698ac31").IsZero() {
		t.Fatal("UUIDv4 was treated as a transcript timestamp")
	}
}

func TestCodexTranscriptPathUsesLocalSessionDate(t *testing.T) {
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
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := codexTranscriptPath(&surface.Session{ID: id}); got != path {
		t.Fatalf("path=%q want=%q", got, path)
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
