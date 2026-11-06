package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const claudeRefusalLines = `{"type":"assistant","uuid":"a1","timestamp":"2026-11-05T09:45:16.000Z","message":{"id":"m1","model":"claude-opus-5-5","role":"assistant","stop_reason":"refusal","type":"message","content":[{"type":"thinking","thinking":""}]}}
{"type":"system","subtype":"informational","level":"notice","timestamp":"2026-11-05T09:45:16.100Z"}
{"type":"user","uuid":"u2","isMeta":true,"timestamp":"2026-11-05T09:45:16.200Z","message":{"role":"user","content":"Your response above was stopped by a safety classifier."}}
{"type":"system","subtype":"model_refusal_no_fallback","level":"warning","timestamp":"2026-11-05T09:45:18.000Z"}
{"type":"assistant","uuid":"a2","timestamp":"2026-11-05T09:45:18.292Z","message":{"id":"m2","model":"<synthetic>","role":"assistant","stop_reason":"refusal","type":"message","content":[{"type":"text","text":"API Error: Opus 5.5's safeguards flagged this session."}]},"error":"invalid_request","isApiErrorMessage":true}`

const claudeServerErrorLine = `{"type":"assistant","uuid":"a1","timestamp":"2026-11-05T09:45:18.292Z","message":{"id":"m1","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","type":"message","content":[{"type":"text","text":"API Error: Server error mid-response."}]},"error":"server_error","isApiErrorMessage":true}`

func claudeTurnFixture(t *testing.T, lines ...string) (*Claude, *surface.Session) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	transcript := filepath.Join(fixture.Root, "projects", "fixture-project", fixtureConversation+".jsonl")
	user := `{"type":"user","uuid":"u1","sessionId":"` + fixtureConversation + `","cwd":"` + fixture.Home + `","timestamp":"2026-11-05T09:45:10Z","message":{"role":"user","content":"keep going"}}`
	if err := os.MkdirAll(filepath.Dir(transcript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(user+"\n"+strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return NewClaude(nil, fixture.Home), &surface.Session{ID: fixtureConversation, Surface: surface.KindClaude, Transcript: transcript}
}

func TestATurnTheSafeguardsRefusedStopsTheSession(t *testing.T) {
	c, sess := claudeTurnFixture(t, claudeRefusalLines)
	observation, err := c.Observe(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	stop := observation.TurnStop
	if stop == nil || !stop.Refused || stop.TurnID == "" || !strings.Contains(stop.Reason, "safeguards flagged this session") || !stop.At.Equal(time.Date(2026, 11, 5, 9, 45, 18, 292000000, time.UTC)) {
		t.Fatalf("stop=%+v", stop)
	}
	if observation.LimitStop != nil || !observation.SettledAt.IsZero() {
		t.Fatalf("a refused turn also reads as limited or settled: limit=%+v settled=%s", observation.LimitStop, observation.SettledAt)
	}
}

func TestATurnAnAPIErrorEndedStopsTheSessionWithoutARefusal(t *testing.T) {
	c, sess := claudeTurnFixture(t, claudeServerErrorLine)
	observation, err := c.Observe(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if stop := observation.TurnStop; stop == nil || stop.Refused || !strings.Contains(stop.Reason, "Server error mid-response") {
		t.Fatalf("stop=%+v", stop)
	}
}

func TestAUsageLimitIsNotATurnStop(t *testing.T) {
	c, sess := claudeTurnFixture(t, claudeUsageLimitLine)
	observation, err := c.Observe(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if observation.TurnStop != nil || observation.LimitStop == nil {
		t.Fatalf("turn stop=%+v limit stop=%+v", observation.TurnStop, observation.LimitStop)
	}
}

func TestATurnThatSucceedsAfterAnAPIErrorEndsTheStop(t *testing.T) {
	c, sess := claudeTurnFixture(t, claudeServerErrorLine,
		`{"type":"user","uuid":"u3","sessionId":"`+fixtureConversation+`","timestamp":"2026-11-05T12:00:00Z","message":{"role":"user","content":"continue"}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-11-05T12:00:05Z","message":{"id":"m3","model":"claude-opus-5-5","role":"assistant","stop_reason":"end_turn","type":"message","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"system","subtype":"turn_duration","timestamp":"2026-11-05T12:00:06Z"}`)
	observation, err := c.Observe(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if observation.TurnStop != nil || observation.SettledAt.IsZero() {
		t.Fatalf("stop=%+v settled=%s", observation.TurnStop, observation.SettledAt)
	}
}
