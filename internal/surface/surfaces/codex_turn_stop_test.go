package surfaces

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func observeCodexTurns(t *testing.T, newestFirst ...map[string]any) *surface.TurnObservation {
	t.Helper()
	data := make([]any, len(newestFirst))
	for index, turn := range newestFirst {
		turn["items"] = []any{}
		data[index] = turn
	}
	startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": "thread", "status": map[string]any{"type": "idle"}}}
		case "thread/turns/list":
			return map[string]any{"data": data}
		}
		return nil
	})
	observation, err := isolatedManagedRuntime(t).Observe(context.Background(), managedSession())
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func TestACodexTurnThatFailedStopsTheSession(t *testing.T) {
	for _, status := range []any{"failed", map[string]any{"type": "failed"}} {
		observation := observeCodexTurns(t, map[string]any{"id": "turn-1", "status": status, "error": map[string]any{"message": "stream disconnected before completion", "codexErrorInfo": map[string]any{"responseStreamDisconnected": map[string]any{"httpStatusCode": 502}}}})
		if stop := observation.TurnStop; stop == nil || stop.TurnID != "turn-1" || stop.Refused || !strings.Contains(stop.Reason, "stream disconnected before completion") {
			t.Fatalf("status=%v stop=%+v", status, stop)
		}
	}
}

func TestACodexTurnThatDidNotFailIsNotAStop(t *testing.T) {
	for name, turn := range map[string]map[string]any{
		"interrupted": {"id": "turn-1", "status": "interrupted"},
		"usage limit": {"id": "turn-1", "status": "failed", "error": map[string]any{"message": "You've hit your usage limit.", "codexErrorInfo": "usageLimitExceeded"}},
		"completed":   {"id": "turn-1", "status": "completed"},
	} {
		if stop := observeCodexTurns(t, turn).TurnStop; stop != nil {
			t.Fatalf("%s: stop=%+v", name, stop)
		}
	}
}

func TestACodexTurnThatSucceedsAfterAFailureEndsTheStop(t *testing.T) {
	observation := observeCodexTurns(t,
		map[string]any{"id": "turn-2", "status": "completed"},
		map[string]any{"id": "turn-1", "status": "failed", "error": map[string]any{"message": "boom"}})
	if observation.TurnStop != nil {
		t.Fatalf("stop=%+v", observation.TurnStop)
	}
}

func TestACodexRolloutWhoseTurnRanOutOfRetriesStopsTheSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	appendLine(t, path, `{"timestamp":"2026-10-21T23:05:36Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`)
	appendLine(t, path, `{"timestamp":"2026-10-21T23:05:37.065Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":null,"error":{"message":"exceeded retry limit, last status: 429 Too Many Requests","codex_error_info":{"response_too_many_failed_attempts":{"http_status_code":429}}}}}`)
	codex := &Codex{}
	local, err := codex.LocalStatus(context.Background(), codexWorkingSession(path, surface.StatusIdle))
	if err != nil || local.TurnStop == nil || local.TurnStop.TurnID != "turn-1" || !strings.Contains(local.TurnStop.Reason, "exceeded retry limit") || local.TurnStop.At.IsZero() {
		t.Fatalf("stop=%+v err=%v", local.TurnStop, err)
	}
	appendLine(t, path, `{"timestamp":"2026-10-21T23:06:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`)
	appendLine(t, path, `{"timestamp":"2026-10-21T23:06:05Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2","last_agent_message":"ok","error":null}}`)
	if local, err = codex.LocalStatus(context.Background(), local); err != nil || local.TurnStop != nil {
		t.Fatalf("a later turn that succeeded still stops the session: stop=%+v err=%v", local.TurnStop, err)
	}
}

func TestACodexRolloutTurnAUsageLimitEndedIsNotAStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	appendLine(t, path, `{"timestamp":"2026-10-21T23:05:36Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`)
	appendLine(t, path, `{"timestamp":"2026-10-21T23:05:37Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","error":{"message":"You've hit your usage limit.","codex_error_info":"usage_limit_exceeded"}}}`)
	local, err := (&Codex{}).LocalStatus(context.Background(), codexWorkingSession(path, surface.StatusIdle))
	if err != nil || local.TurnStop != nil {
		t.Fatalf("stop=%+v err=%v", local.TurnStop, err)
	}
}
