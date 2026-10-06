package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObserveClaudeRunsReadsTypedRunFields(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "jobs", "job-123", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"template":        "bg",
		"state":           "blocked",
		"sessionId":       "session-123",
		"resumeSessionId": "session-123",
		"createdAt":       "2026-10-04T12:00:00.123Z",
		"updatedAt":       "2026-10-04T12:01:00.456Z",
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := ObserveClaudeRuns(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("observations=%+v", got)
	}
	if got[0].JobID != "job-123" || got[0].RunType != "bg" || got[0].ProviderState != "blocked" || got[0].SessionID != "session-123" {
		t.Fatalf("observation=%+v", got[0])
	}
	if !got[0].CreatedAt.Equal(time.Date(2026, 10, 4, 12, 0, 0, 123000000, time.UTC)) || !got[0].UpdatedAt.Equal(time.Date(2026, 10, 4, 12, 1, 0, 456000000, time.UTC)) {
		t.Fatalf("timestamps=%+v", got[0])
	}
}

func TestObserveClaudeConfiguredContextWindowRequiresExplicitLaunchValue(t *testing.T) {
	if got := ObserveClaudeConfiguredContextWindow(""); got.Source != ClaudeContextWindowSourceUnknown || got.Window != 0 || got.Reliable {
		t.Fatalf("empty model observation=%+v", got)
	}
	if got := ObserveClaudeConfiguredContextWindow("claude-opus-5-5"); got.Source != ClaudeContextWindowSourceUnknown || got.Window != 0 || got.Reliable {
		t.Fatalf("plain model observation=%+v", got)
	}
	if got := ObserveClaudeConfiguredContextWindow("claude-opus-5-5[1m]"); got.Source != ClaudeContextWindowSourceConfigured || got.Window != 1_000_000 || !got.Reliable {
		t.Fatalf("configured model observation=%+v", got)
	}
}

func TestObserveClaudeRunsDoesNotInferUnavailableWakeFields(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "jobs", "job-123", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"template":"bg","state":"blocked","detail":"waiting for review","updatedAt":"2026-10-04T12:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := ObserveClaudeRuns(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProviderState != "blocked" {
		t.Fatalf("observations=%+v", got)
	}
	encoded, err := json.Marshal(got[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"waiting", "wakeAt"} {
		if _, found := fields[field]; found {
			t.Fatalf("inferred unavailable %s field: %s", field, encoded)
		}
	}
}

func TestObserveClaudeRunsRejectsMalformedProviderTimestamp(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "jobs", "job-123", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"updatedAt":"not-a-time"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveClaudeRuns(context.Background(), home); err == nil {
		t.Fatal("expected malformed timestamp error")
	}
}
