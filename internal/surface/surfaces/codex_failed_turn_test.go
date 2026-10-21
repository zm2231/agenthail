package surfaces

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexCapacityFailedTurn = `{"timestamp":"2026-10-21T03:32:36Z","type":"session_meta","payload":{"id":"thread"}}
{"timestamp":"2026-10-21T03:32:36Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-10-21T03:32:36Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-fixture"}}
{"timestamp":"2026-10-21T03:32:36Z","type":"event_msg","payload":{"type":"user_message","message":"print the token"}}
{"timestamp":"2026-10-21T03:32:38Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":null,"error":{"message":"Selected model is at capacity. Please try a different model.","codex_error_info":"server_overloaded"},"duration_ms":1758}}
{"timestamp":"2026-10-21T03:33:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-10-21T03:33:00Z","type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt-fixture"}}
{"timestamp":"2026-10-21T03:33:00Z","type":"event_msg","payload":{"type":"user_message","message":"try again"}}
{"timestamp":"2026-10-21T03:33:01Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
{"timestamp":"2026-10-21T03:33:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2","last_agent_message":"ok","error":null,"duration_ms":2000}}
`

func TestCodexTurnRefusedByTheModelReadsAsFailed(t *testing.T) {
	path := timelineFixture(t, codexCapacityFailedTurn)
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	read, err := codex.ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil || len(read.Exchanges) != 2 {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	if turn := read.Exchanges[0].Turn; turn == nil || turn.State != surface.TurnFailed {
		t.Fatalf("refused turn=%+v, want failed", turn)
	}
	if turn := read.Exchanges[1].Turn; turn == nil || turn.State != surface.TurnCompleted {
		t.Fatalf("answered turn=%+v, want completed: a null error is no error", turn)
	}
	notices := 0
	for _, item := range read.Items {
		if item.Kind == "notice" {
			notices++
			if item.Title != "Codex error" || item.Status != "server_overloaded" || !strings.Contains(item.Text, "at capacity") || item.TurnID != "turn-1" {
				t.Fatalf("notice=%+v", item)
			}
		}
	}
	if notices != 1 {
		t.Fatalf("notices=%d, want the refusal shown once", notices)
	}
}

func TestCodexStreamEndsARefusedTurnAsAFailureWithItsReason(t *testing.T) {
	path := timelineFixture(t, codexCapacityFailedTurn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var done []surface.StreamEvent
	err := (&Codex{}).Stream(ctx, &surface.Session{ID: "thread", Transcript: path, TranscriptOffset: 0, TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			done = append(done, event)
			if len(done) == 2 {
				cancel()
			}
		}
	}, 2*time.Second)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(done) != 2 {
		t.Fatalf("done events=%d", len(done))
	}
	if !done[0].Failed() || done[0].Title != "Turn failed" || !strings.Contains(done[0].Text, "at capacity") || done[0].Turn == nil || done[0].Turn.State != surface.TurnFailed {
		t.Fatalf("refused turn done=%+v", done[0])
	}
	if done[1].Failed() || done[1].Turn == nil || done[1].Turn.State != surface.TurnCompleted {
		t.Fatalf("answered turn done=%+v", done[1])
	}
}
