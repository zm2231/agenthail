package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func writeRollout(t *testing.T, home, day, name string, written time.Time, lines ...string) string {
	t.Helper()
	path := filepath.Join(home, "sessions", day, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	return path
}

func tokenCount(at string, rateLimits string) string {
	return `{"timestamp":"` + at + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10}},"rate_limits":` + rateLimits + `}}`
}

func TestCodexUsageReadsTheNewestRolloutThatRecordsLimits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	now := time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	writeRollout(t, home, "2026/10/20", "rollout-old.jsonl", now.Add(-72*time.Hour),
		tokenCount("2026-10-20T10:00:00Z", `{"primary":{"used_percent":5.0,"window_minutes":10080,"resets_at":1793293773},"secondary":null}`))
	writeRollout(t, home, "2026/10/22", "rollout-long.jsonl", now.Add(-time.Hour),
		tokenCount("2026-10-22T09:00:00Z", `{"primary":{"used_percent":12.0,"window_minutes":10080,"resets_at":1793293773},"secondary":null}`),
		tokenCount("2026-10-23T13:00:00Z", `{"primary":{"used_percent":61.5,"window_minutes":10080,"resets_at":1793293773},"secondary":{"used_percent":40.0,"window_minutes":300,"resets_at":1793200000}}`))
	writeRollout(t, home, "2026/10/23", "rollout-new.jsonl", now,
		`{"timestamp":"2026-10-23T13:59:00Z","type":"session_meta","payload":{}}`)

	usage, err := NewCodex("").Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []surface.UsageWindow{
		{Kind: "5h", Percent: 40, ResetsAt: time.Unix(1793200000, 0).UTC()},
		{Kind: "weekly", Percent: 61.5, ResetsAt: time.Unix(1793293773, 0).UTC()},
	}
	if !usage.FetchedAt.Equal(time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)) || len(usage.Limits) != 2 || usage.Limits[0] != want[0] || usage.Limits[1] != want[1] {
		t.Fatalf("a rollout started yesterday and still written gives the newest reading, shortest window first: %+v", usage)
	}
}

func TestCodexUsageSaysWhenNoRolloutRecordsLimits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if _, err := NewCodex("").Usage(context.Background()); !errors.Is(err, ErrNoCodexUsage) {
		t.Fatalf("an empty Codex home: err=%v", err)
	}
	writeRollout(t, home, "2026/10/23", "rollout.jsonl", time.Now(), tokenCount("2026-10-23T13:00:00Z", `{"primary":null,"secondary":null}`))
	if _, err := NewCodex("").Usage(context.Background()); !errors.Is(err, ErrNoCodexUsage) {
		t.Fatalf("a rollout whose token counts carry no window: err=%v", err)
	}
}

func TestCodexUsageRereadsARolloutOnlyWhenItGrows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := writeRollout(t, home, "2026/10/23", "rollout.jsonl", time.Now(), tokenCount("2026-10-23T13:00:00Z", `{"primary":{"used_percent":19.0,"window_minutes":10080,"resets_at":1793293773}}`))
	codex := NewCodex("")
	if usage, err := codex.Usage(context.Background()); err != nil || usage.Limits[0].Percent != 19 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	appendTranscript(t, path, tokenCount("2026-10-23T13:30:00Z", `{"primary":{"used_percent":23.0,"window_minutes":10080,"resets_at":1793293773}}`)+"\n")
	if usage, err := codex.Usage(context.Background()); err != nil || usage.Limits[0].Percent != 23 {
		t.Fatalf("after a new token count usage=%+v err=%v", usage, err)
	}
}
