package codexsurface

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func TestARolloutSegmentPageAboveAFloorMatchesAReadOfTheWholeBudget(t *testing.T) {
	surfacetest.ScaleTranscriptReads(t)
	written := surfacetest.WritePageTranscript(t, codexPageRecords, 20000/16, 6)
	rng := rand.New(rand.NewSource(8))
	for sample := 0; sample < 12; sample++ {
		high := len(written.Boundaries)/2 + rng.Intn(len(written.Boundaries)/2)
		before := written.Boundaries[high]
		floor := written.Boundaries[rng.Intn(len(written.Boundaries)/2)]
		if sample%2 == 1 {
			// A floor inside the first window, so the page stops at the floor.
			floor = written.Boundaries[high-1-rng.Intn(200)]
		}
		read := func(window int64) *surface.SessionReadResult {
			previous := transcript.TimelineFirstWindow
			transcript.TimelineFirstWindow = window
			defer func() { transcript.TimelineFirstWindow = previous }()
			page, err := transcript.ReadWindow(context.Background(), written.Path, codexTranscript{}, floor, before, 0)
			if err != nil {
				t.Fatal(err)
			}
			return page
		}
		surfacetest.SamePage(t, fmt.Sprintf("floor=%d before=%d", floor, before), read(transcript.TimelineFirstWindow), read(transcript.TimelineReadBudget))
	}
}

func codexPageRecords(turn int, rng *rand.Rand) []map[string]any {
	at := fmt.Sprintf("2026-10-23T09:%02d:%02dZ", turn/60%60, turn%60)
	id := fmt.Sprintf("turn-%d", turn)
	records := []map[string]any{
		{"timestamp": at, "type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": id}},
		{"timestamp": at, "type": "turn_context", "payload": map[string]any{"turn_id": id, "model": "gpt-fixture"}},
		{"timestamp": at, "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": fmt.Sprintf("question %d", turn)}},
		{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": fmt.Sprintf("question %d", turn)}}}},
	}
	for call := 0; call < rng.Intn(4); call++ {
		callID := fmt.Sprintf("call-%d-%d", turn, call)
		records = append(records,
			map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "function_call", "name": "shell", "call_id": callID, "arguments": `{"command":"go test"}`}},
			map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": callID, "output": surfacetest.ToolOutput(rng)}},
		)
	}
	return append(records,
		map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": fmt.Sprintf("answer %d", turn)}}}},
		map[string]any{"timestamp": at, "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": id, "duration_ms": 1000}},
	)
}

func TestACodexTranscriptPageMatchesAReadOfTheWholeBudget(t *testing.T) {
	surfacetest.ScaleTranscriptReads(t)
	written := surfacetest.WritePageTranscript(t, codexPageRecords, 20000/16, 2)
	surfacetest.CheckPagesMatchTheWholeBudgetRead(t, written, codexTranscript{}, "codex", 16)
}
