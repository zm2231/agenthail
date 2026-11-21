package claudesurface

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func claudePageRecords(turn int, rng *rand.Rand) []map[string]any {
	at := fmt.Sprintf("2026-10-23T09:%02d:%02dZ", turn/60%60, turn%60)
	records := []map[string]any{{"type": "user", "uuid": fmt.Sprintf("user-%d", turn), "timestamp": at, "message": map[string]any{"role": "user", "content": fmt.Sprintf("question %d", turn)}}}
	for call := 0; call < rng.Intn(4); call++ {
		id := fmt.Sprintf("call-%d-%d", turn, call)
		records = append(records,
			map[string]any{"type": "assistant", "uuid": "tool-" + id, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "go test"}}}}},
			map[string]any{"type": "user", "uuid": "result-" + id, "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": surfacetest.ToolOutput(rng)}}}},
		)
	}
	return append(records,
		map[string]any{"type": "assistant", "uuid": fmt.Sprintf("answer-%d", turn), "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("answer %d", turn)}}}},
		map[string]any{"type": "system", "subtype": "turn_duration", "timestamp": at, "durationMs": 1000},
	)
}

func TestATranscriptPageReadsWhatItShowsAndMatchesAReadOfTheWholeBudget(t *testing.T) {
	surfacetest.ScaleTranscriptReads(t)
	written := surfacetest.WritePageTranscript(t, claudePageRecords, 26000/16, 1)
	surfacetest.CheckPagesMatchTheWholeBudgetRead(t, written, claudeTranscript{}, "claude", 24)
}

func TestAPageWhoseToolOutputIsLargerThanTheFirstWindowStillShowsIt(t *testing.T) {
	surfacetest.ScaleTranscriptReads(t)
	huge := func(turn int, rng *rand.Rand) []map[string]any {
		records := claudePageRecords(turn, rng)
		if turn == 30 || turn == 60 {
			at := records[0]["timestamp"]
			id := fmt.Sprintf("huge-%d", turn)
			records = append(records[:1], append([]map[string]any{
				{"type": "assistant", "uuid": "tool-" + id, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "cat log"}}}}},
				{"type": "user", "uuid": "result-" + id, "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("x", int(3*transcript.TimelineFirstWindow))}}}},
			}, records[1:]...)...)
		}
		return records
	}
	written := surfacetest.WritePageTranscript(t, huge, 60, 3)
	for _, limit := range []int{1, 2, 10} {
		shown := 0
		for before, pages := int64(0), 0; pages < 100; pages++ {
			got := surfacetest.ReadPageWithFirstWindow(t, transcript.TimelineFirstWindow, written.Path, claudeTranscript{}, before, limit)
			surfacetest.SamePage(t, fmt.Sprintf("limit=%d before=%d", limit, before), got, surfacetest.ReadPageWithFirstWindow(t, transcript.TimelineReadBudget, written.Path, claudeTranscript{}, before, limit))
			for _, item := range got.Items {
				if strings.HasPrefix(item.Text, "xxxx") {
					shown++
				}
			}
			if got.NextBefore <= 0 {
				break
			}
			before = got.NextBefore
		}
		if shown != 2 {
			t.Fatalf("limit=%d: paging the transcript showed %d of its 2 oversized tool outputs", limit, shown)
		}
	}
}

func TestAPageIsTruncatedOnlyByWhatItShows(t *testing.T) {
	older := func(turn int, rng *rand.Rand) []map[string]any {
		records := claudePageRecords(turn, rng)[:1]
		at := records[0]["timestamp"]
		id := fmt.Sprintf("call-%d", turn)
		output := "ok"
		if turn == 1 {
			output = strings.Repeat("x", 2*transcript.TimelineTextBudget)
		}
		return append(records,
			map[string]any{"type": "assistant", "uuid": "tool-" + id, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "go test"}}}}},
			map[string]any{"type": "user", "uuid": "result-" + id, "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": output}}}},
			map[string]any{"type": "assistant", "uuid": fmt.Sprintf("answer-%d", turn), "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("answer %d", turn)}}}},
		)
	}
	written := surfacetest.WritePageTranscript(t, older, 3, 4)
	if newest := surfacetest.ReadPageWithFirstWindow(t, transcript.TimelineFirstWindow, written.Path, claudeTranscript{}, 0, 1); newest.Truncated {
		t.Fatal("the newest exchange is marked truncated by an older output it does not show")
	}
	if whole := surfacetest.ReadPageWithFirstWindow(t, transcript.TimelineFirstWindow, written.Path, claudeTranscript{}, 0, 3); !whole.Truncated {
		t.Fatal("a page showing the shortened output is not marked truncated")
	}
}

func TestTheReplyOfAPageWhoseLatestTurnsHaveNoAnswerMatchesAReadOfTheWholeBudget(t *testing.T) {
	quiet := func(turn int, rng *rand.Rand) []map[string]any {
		records := claudePageRecords(turn, rng)
		if turn > 2000 {
			return records[:1]
		}
		if turn > 1990 {
			at := records[0]["timestamp"]
			return append(records, map[string]any{"type": "user", "uuid": fmt.Sprintf("result-filler-%d", turn), "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "none", "content": strings.Repeat("f", int(transcript.TimelineFirstWindow)/8)}}}})
		}
		return records
	}
	written := surfacetest.WritePageTranscript(t, quiet, 2040, 5)
	for _, limit := range []int{1, 2, 10} {
		got := surfacetest.ReadPageWithFirstWindow(t, transcript.TimelineFirstWindow, written.Path, claudeTranscript{}, 0, limit)
		surfacetest.SamePage(t, fmt.Sprintf("limit=%d", limit), got, surfacetest.ReadPageWithFirstWindow(t, transcript.TimelineReadBudget, written.Path, claudeTranscript{}, 0, limit))
		if got.Reply == nil || got.Reply.Text != "answer 2000" {
			t.Fatalf("limit=%d: reply=%+v, want the last answer, from turn 2000", limit, got.Reply)
		}
	}
}
