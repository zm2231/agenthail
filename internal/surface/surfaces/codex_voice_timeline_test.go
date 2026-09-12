package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexTimelineRetainsVoiceAlongsideAgentTools(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-09-12T01:00:00Z","type":"realtime_item","payload":{"id":"start","type":"realtime_session_started","realtime_session_id":"voice"}}
{"timestamp":"2026-09-12T01:00:01Z","type":"realtime_item","payload":{"id":"utterance","type":"transcript_segment","role":"user","text":"Check the builder","realtime_session_id":"voice"}}
{"timestamp":"2026-09-12T01:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"tool","arguments":"agenthail list --json"}}
{"timestamp":"2026-09-12T01:00:03Z","type":"realtime_item","payload":{"id":"reply","type":"transcript_segment","role":"assistant","text":"The builder is working.","realtime_session_id":"voice"}}
{"timestamp":"2026-09-12T01:00:04Z","type":"realtime_item","payload":{"id":"close","type":"realtime_session_closed","outcome":"ended","realtime_session_id":"voice"}}
`)
	page, err := (&Codex{}).Timeline(context.Background(), &surface.Session{Transcript: path}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("voice history missing: %+v", page)
	}
	if page.Items[1].Role != "user" || page.Items[1].Text != "Check the builder" || page.Items[2].Kind != "toolCall" || page.Items[3].Role != "assistant" || page.Items[4].Status != "ended" {
		t.Fatalf("voice history changed: %+v", page.Items)
	}
	again, err := (&Codex{}).Timeline(context.Background(), &surface.Session{Transcript: path}, 0)
	if err != nil || again.Items[1].ID != page.Items[1].ID || page.Items[1].Timestamp == "" {
		t.Fatal("voice history identity or timestamp missing")
	}
}
