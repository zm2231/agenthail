package claudesurface

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestClaudeContextUsageTracksCompactionAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	surfacetest.WriteTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":129000,"output_tokens":500}}}`,
		`{"type":"user","timestamp":"2026-07-16T01:01:00Z","message":{"content":"/compact"}}`,
		`{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-16T01:01:02Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
	)
	adapter := NewClaude(nil, t.TempDir())
	session := &surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: path}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if usage.UsedTokens != 42000 || usage.ContextWindow != 0 || usage.WindowEstimated || usage.CompactionCount != 1 || usage.ReclaimedTokens != 108000 || usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
	testkit.AppendLines(t, path, `{"type":"user","timestamp":"2026-07-16T01:02:00Z","message":{"content":"/compact"}}`)
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
	testkit.AppendLines(t, path,
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
	testkit.AppendLines(t, path, `{"type":"user","timestamp":"2026-07-16T01:03:00Z","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}`)
	usage, err = adapter.ContextUsage(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !usage.Compacting {
		t.Fatalf("usage=%+v", usage)
	}
	testkit.AppendLines(t, path, `{"type":"system","subtype":"local_command","timestamp":"2026-07-16T01:03:03Z","content":"done"}`)
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
	surfacetest.WriteTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":249000,"output_tokens":500}}}`,
	)
	adapter := NewClaude(nil, t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.UsedTokens != 250000 || usage.ContextWindow != 0 || usage.WindowEstimated {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageUsesExplicitConfiguredWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	surfacetest.WriteTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":249000,"output_tokens":500}}}`,
	)
	adapter := NewClaude(nil, t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: path, ConfiguredModel: "claude-opus-5-5[1m]"})
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.ContextWindow != 1_000_000 || usage.ContextWindowSource != ClaudeContextWindowSourceConfigured || usage.WindowEstimated {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageInvalidatesStaleConfiguredWindowAfterObservedModelSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	surfacetest.WriteTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-5-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":249000,"output_tokens":500}}}`,
	)
	adapter := NewClaude(nil, t.TempDir())
	session := &surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: path, ConfiguredModel: "claude-opus-5-5[1m]"}
	usage, err := adapter.ContextUsage(context.Background(), session)
	if err != nil || usage.ContextWindow != 1_000_000 {
		t.Fatalf("initial usage=%+v err=%v", usage, err)
	}
	testkit.AppendLines(t, path,
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

func TestClaudeContextUsageCompactCommandEndsAtBoundary(t *testing.T) {
	usageRecord := `{"type":"assistant","timestamp":"2026-07-16T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":20000,"cache_read_input_tokens":129000,"output_tokens":500}}}`
	boundary := `{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-16T01:01:02Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`
	for name, records := range map[string][]string{
		"older command marker written after the boundary": {
			usageRecord,
			boundary,
			`{"type":"user","timestamp":"2026-07-16T01:01:00Z","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}`,
		},
		"command and marker completed by the boundary": {
			usageRecord,
			`{"type":"user","timestamp":"2026-07-16T01:01:00Z","message":{"content":"/compact"}}`,
			`{"type":"user","timestamp":"2026-07-16T01:01:00.001Z","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}`,
			boundary,
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "claude.jsonl")
			surfacetest.WriteTestTranscript(t, path, records...)
			usage, err := NewClaude(nil, t.TempDir()).ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: path})
			if err != nil {
				t.Fatal(err)
			}
			if usage.Compacting {
				t.Fatalf("usage=%+v", usage)
			}
		})
	}
}

func TestClaudeContextUsageDeduplicatesReplayedRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	surfacetest.WriteTestTranscript(t, path,
		`{"type":"assistant","timestamp":"2026-06-29T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":149000,"output_tokens":500}}}`,
		`{"uuid":"boundary-old","type":"system","subtype":"compact_boundary","timestamp":"2026-06-29T01:01:00Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
		`{"uuid":"boundary-new","type":"system","subtype":"compact_boundary","timestamp":"2026-06-30T01:01:00Z","compactMetadata":{"preTokens":151000,"postTokens":41000}}`,
		`{"type":"assistant","timestamp":"2026-07-05T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":173446,"output_tokens":500}}}`,
		`{"uuid":"boundary-old","type":"system","subtype":"compact_boundary","timestamp":"2026-06-29T01:01:00Z","compactMetadata":{"preTokens":150000,"postTokens":42000}}`,
		`{"type":"assistant","timestamp":"2026-05-11T01:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":47192,"output_tokens":500}}}`,
	)
	adapter := NewClaude(nil, t.TempDir())
	usage, err := adapter.ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CompactionCount != 2 || usage.UsedTokens != 174446 || usage.PreCompactTokens != 151000 || usage.PostCompactTokens != 41000 || !usage.LastCompactedAt.Equal(time.Date(2026, 6, 30, 1, 1, 0, 0, time.UTC)) {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestClaudeContextUsageReadsTheWindowClaudeRequested(t *testing.T) {
	request := func(model string, betas ...string) string {
		encoded, _ := json.Marshal(map[string]any{"type": "api-request", "timestamp": "2026-10-20T10:00:00Z", "querySource": "repl_main_thread", "params": map[string]any{"model": model, "betas": append([]string{"claude-code-20250219"}, betas...)}})
		return string(encoded)
	}
	reply := func(requested, model string) string {
		record := map[string]any{"type": "assistant", "timestamp": "2026-10-20T10:00:01Z", "message": map[string]any{"model": model, "usage": map[string]any{"input_tokens": 1000, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 299000, "output_tokens": 500}}}
		if requested != "" {
			record["requestedModel"] = requested
		}
		encoded, _ := json.Marshal(record)
		return string(encoded)
	}
	for _, test := range []struct {
		name       string
		configured string
		records    []string
		window     int64
		source     string
	}{
		{"requested 1M model", "", []string{reply("claude-opus-5-5[1m]", "claude-opus-5-5")}, 1_000_000, ClaudeContextWindowSourceTranscript},
		{"requested plain model", "", []string{reply("claude-opus-5-5", "claude-opus-5-5")}, 0, ClaudeContextWindowSourceUnknown},
		{"requested plain model after a 1M launch", "claude-opus-5-5[1m]", []string{reply("claude-opus-5-5", "claude-opus-5-5")}, 0, ClaudeContextWindowSourceUnknown},
		{"requested 1M for another model", "", []string{reply("claude-sonnet-5-5[1m]", "claude-opus-5-5")}, 0, ClaudeContextWindowSourceUnknown},
		{"request with the 1M beta", "", []string{request("claude-opus-5-5", "context-1m-2025-08-07"), reply("claude-opus-5-5", "claude-opus-5-5")}, 1_000_000, ClaudeContextWindowSourceRequest},
		{"request without the 1M beta", "", []string{request("claude-opus-5-5"), reply("claude-opus-5-5", "claude-opus-5-5")}, 0, ClaudeContextWindowSourceUnknown},
		{"request without the 1M beta after a 1M launch", "claude-opus-5-5[1m]", []string{request("claude-opus-5-5"), reply("", "claude-opus-5-5")}, 1_000_000, ClaudeContextWindowSourceConfigured},
		{"newer request without the 1M beta", "", []string{request("claude-opus-5-5", "context-1m-2025-08-07"), request("claude-opus-5-5"), reply("claude-opus-5-5", "claude-opus-5-5")}, 0, ClaudeContextWindowSourceUnknown},
		{"side request after the 1M request", "", []string{request("claude-opus-5-5", "context-1m-2025-08-07"), request("claude-haiku-5-5"), reply("", "claude-opus-5-5")}, 1_000_000, ClaudeContextWindowSourceRequest},
		{"1M side request", "", []string{request("claude-haiku-5-5", "context-1m-2025-08-07"), request("claude-opus-5-5"), reply("claude-opus-5-5", "claude-opus-5-5")}, 0, ClaudeContextWindowSourceUnknown},
		{"reply without a model", "", []string{request("claude-opus-5-5", "context-1m-2025-08-07"), reply("claude-opus-5-5[1m]", "")}, 0, ClaudeContextWindowSourceUnknown},
		{"no requested model", "claude-opus-5-5[1m]", []string{reply("", "claude-opus-5-5")}, 1_000_000, ClaudeContextWindowSourceConfigured},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "claude.jsonl")
			surfacetest.WriteTestTranscript(t, path, test.records...)
			usage, err := NewClaude(nil, t.TempDir()).ContextUsage(context.Background(), &surface.Session{ID: "claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: path, ConfiguredModel: test.configured})
			if err != nil || usage == nil || usage.UsedTokens != 300000 || usage.ContextWindow != test.window || usage.ContextWindowSource != test.source || usage.WindowEstimated {
				t.Fatalf("usage=%+v err=%v", usage, err)
			}
		})
	}
}
