package codexsurface

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func TestCodexContextUsageIgnoresPartialRecordUntilComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	surfacetest.WriteTestTranscript(t, path, codexTokenRecord("2026-07-16T04:29:00Z", 120000, 120000))
	partial := codexTokenRecord("2026-07-16T04:30:00Z", 130000, 250000)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(partial[:len(partial)/2]); err != nil {
		t.Fatal(err)
	}
	file.Close()
	adapter := surfacetest.InDaemon(NewCodex(""))
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

func TestCodexContextUsageResetsWhenTranscriptIsReplacedAtSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	surfacetest.WriteTestTranscript(t, path, codexTokenRecord("2026-07-16T04:29:00Z", 100, 100))
	adapter := surfacetest.InDaemon(NewCodex(""))
	session := &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil || usage == nil || usage.UsedTokens != 100 {
		t.Fatalf("initial usage=%+v err=%v", usage, err)
	}
	replacement := filepath.Join(t.TempDir(), "replacement.jsonl")
	surfacetest.WriteTestTranscript(t, replacement, codexTokenRecord("2026-07-16T04:30:00Z", 200, 200))
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
	surfacetest.WriteTestTranscript(t, path,
		codexTokenRecord("2026-07-16T04:29:00Z", 180000, 900000),
		`{"timestamp":"2026-07-16T04:30:00Z","type":"compacted","payload":{"window_number":1,"window_id":"019f6930-0000-7000-8000-000000000000"}}`,
		codexTokenRecord("2026-07-16T04:30:01Z", 80000, 980000),
		codexTokenRecord("2026-07-16T04:31:00Z", 100000, 1000000),
		`{"timestamp":"2026-07-16T04:32:00Z","type":"compacted","payload":{"window_number":2,"window_id":"019f6931-d4c0-7000-8000-000000000000"}}`,
		codexTokenRecord("2026-07-16T04:32:01Z", 40000, 1040000),
	)
	adapter := surfacetest.InDaemon(NewCodex(""))
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CompactionCount != 2 || usage.PreCompactTokens != 100000 || usage.PostCompactTokens != 40000 || usage.ReclaimedTokens != 60000 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestCodexContextUsageUsesLatestContextSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	surfacetest.WriteTestTranscript(t, path,
		codexTokenRecord("2026-07-16T04:29:00Z", 180000, 900000),
		`{"timestamp":"2026-07-16T04:30:11Z","type":"compacted","payload":{"window_number":3,"window_id":"019f6930-7747-7c63-9442-3c742e0589f0"}}`,
		codexTokenRecord("2026-07-16T04:30:13Z", 80000, 980000),
	)
	adapter := surfacetest.InDaemon(NewCodex(""))
	session := &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedTokens != 80000 || usage.ContextWindow != 258400 || usage.CumulativeTokens != 980000 || usage.CompactionCount != 3 || usage.PreCompactTokens != 180000 || usage.PostCompactTokens != 80000 || usage.ReclaimedTokens != 100000 || usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
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

func TestCodexReadSessionFindsTranscriptByLocalSessionDate(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("EDT", -4*60*60)
	t.Cleanup(func() { time.Local = originalLocal })
	home := t.TempDir()
	codex := &Codex{home: CodexHome{Codex: home}}
	id := uuidV7ForTest(time.Date(2026, 6, 23, 1, 30, 0, 0, time.UTC))
	path := filepath.Join(home, "sessions", "2026", "06", "22", "rollout-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	surfacetest.WriteTestTranscript(t, path, `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"found"}]}}`)
	read, err := codex.ReadSession(context.Background(), &surface.Session{ID: id}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Items) != 1 || read.Items[0].Text != "found" {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	notV7 := "1845b4c6-eb75-4e59-b439-b95c3698ac31"
	read, err = codex.ReadSession(context.Background(), &surface.Session{ID: notV7}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Items) != 0 || read.UnavailableReason == "" {
		t.Fatalf("UUIDv4 was treated as a transcript timestamp: read=%+v err=%v", read, err)
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

// liveContext drives managed Streams for a session with a local transcript.
// Each Stream first polls the transcript, which sets the baseline, and then
// receives the notifications.
type liveContext struct {
	codex   *Codex
	fake    *codextest.Daemon
	session *surface.Session
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
	events := streamManagedState(t, l.codex, l.fake, l.session, func() {
		for _, notification := range notifications {
			l.fake.Notify(notification.method, notification.params)
		}
	}, func(events []surface.StreamEvent) bool { return len(events) >= len(notifications) })
	last := events[len(events)-1]
	if last.Kind != "context" || last.Context == nil {
		t.Fatalf("events=%+v", events)
	}
	return last.Context
}

type liveNotification struct {
	method string
	params map[string]any
}

func newLiveContext(t *testing.T, records ...string) *liveContext {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex.jsonl")
	surfacetest.WriteTestTranscript(t, path, records...)
	fake := startManagedCodex(t, nil)
	live := &liveContext{
		codex:   isolatedManagedRuntime(t),
		fake:    fake,
		session: &surface.Session{ID: "019f6930-0000-7000-8000-000000000000", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: codexTransportManaged, Transcript: path, TranscriptOffsetSet: true},
	}
	live.fake.AddThread(codextest.Thread{ID: live.session.ID})
	live.fake.Load(live.session.ID)
	return live
}

func uuidV7ForTest(at time.Time) string {
	prefix := fmt.Sprintf("%012x", at.UnixMilli())
	return prefix[:8] + "-" + prefix[8:] + "-7000-8000-000000000000"
}

func codexTokenRecord(timestamp string, current, cumulative int64) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":%d},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":10,"output_tokens":20,"reasoning_output_tokens":5,"total_tokens":%d},"model_context_window":258400}}}`, timestamp, cumulative-20, cumulative, current-20, current)
}
