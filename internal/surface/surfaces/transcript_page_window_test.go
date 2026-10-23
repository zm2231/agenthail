package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type pageTranscript struct {
	path       string
	boundaries []int64
}

func writePageTranscript(t *testing.T, records func(turn int, rng *rand.Rand) []map[string]any, turns int, seed int64) pageTranscript {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var offset int64
	boundaries := []int64{0}
	var buffer strings.Builder
	for turn := 1; turn <= turns; turn++ {
		for _, record := range records(turn, rng) {
			line, _ := json.Marshal(record)
			buffer.Write(line)
			buffer.WriteByte('\n')
			offset += int64(len(line) + 1)
			boundaries = append(boundaries, offset)
		}
		if buffer.Len() > 1<<20 {
			if _, err := file.WriteString(buffer.String()); err != nil {
				t.Fatal(err)
			}
			buffer.Reset()
		}
	}
	if _, err := file.WriteString(buffer.String()); err != nil {
		t.Fatal(err)
	}
	return pageTranscript{path: path, boundaries: boundaries}
}

func toolOutput(rng *rand.Rand) string {
	switch roll := rng.Intn(1000); {
	case roll == 0:
		return strings.Repeat("o", 700<<10+rng.Intn(2<<20))
	case roll < 30:
		return strings.Repeat("o", 20<<10+rng.Intn(200<<10))
	default:
		return strings.Repeat("o", rng.Intn(400))
	}
}

func claudePageRecords(turn int, rng *rand.Rand) []map[string]any {
	at := fmt.Sprintf("2026-10-23T09:%02d:%02dZ", turn/60%60, turn%60)
	records := []map[string]any{{"type": "user", "uuid": fmt.Sprintf("user-%d", turn), "timestamp": at, "message": map[string]any{"role": "user", "content": fmt.Sprintf("question %d", turn)}}}
	for call := 0; call < rng.Intn(4); call++ {
		id := fmt.Sprintf("call-%d-%d", turn, call)
		records = append(records,
			map[string]any{"type": "assistant", "uuid": "tool-" + id, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "go test"}}}}},
			map[string]any{"type": "user", "uuid": "result-" + id, "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": toolOutput(rng)}}}},
		)
	}
	return append(records,
		map[string]any{"type": "assistant", "uuid": fmt.Sprintf("answer-%d", turn), "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("answer %d", turn)}}}},
		map[string]any{"type": "system", "subtype": "turn_duration", "timestamp": at, "durationMs": 1000},
	)
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
			map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": callID, "output": toolOutput(rng)}},
		)
	}
	return append(records,
		map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": fmt.Sprintf("answer %d", turn)}}}},
		map[string]any{"timestamp": at, "type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": id, "duration_ms": 1000}},
	)
}

func readPageWithFirstWindow(t *testing.T, window int64, path, source string, before int64, limit int) *surface.SessionReadResult {
	t.Helper()
	previous := timelineFirstWindow
	timelineFirstWindow = window
	defer func() { timelineFirstWindow = previous }()
	page, err := readTranscriptPage(context.Background(), path, source, before, limit, nil)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func samePage(t *testing.T, label string, got, want *surface.SessionReadResult) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		var differs []string
		gotValue, wantValue := reflect.ValueOf(*got), reflect.ValueOf(*want)
		for i := 0; i < gotValue.NumField(); i++ {
			if !reflect.DeepEqual(gotValue.Field(i).Interface(), wantValue.Field(i).Interface()) {
				gotJSON, _ := json.Marshal(gotValue.Field(i).Interface())
				wantJSON, _ := json.Marshal(wantValue.Field(i).Interface())
				differs = append(differs, fmt.Sprintf("%s: got %.300s want %.300s", gotValue.Type().Field(i).Name, gotJSON, wantJSON))
			}
		}
		t.Fatalf("%s: a page read from a small first window differs from one read of the whole budget:\n%s", label, strings.Join(differs, "\n"))
	}
}

func checkPagesMatchTheWholeBudgetRead(t *testing.T, transcript pageTranscript, source string, samples int) {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	info, err := os.Stat(transcript.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= timelineReadBudget {
		t.Fatalf("fixture is %d bytes; it must exceed the %d byte budget", info.Size(), timelineReadBudget)
	}
	limits := []int{1, 2, 3, 5, 10, 20, 40, surface.MaxExchangePage}
	for sample := 0; sample < samples; sample++ {
		before := int64(0)
		if sample > 0 {
			before = transcript.boundaries[1+rng.Intn(len(transcript.boundaries)-1)]
		}
		limit := limits[rng.Intn(len(limits))]
		want := readPageWithFirstWindow(t, timelineReadBudget, transcript.path, source, before, limit)
		got := readPageWithFirstWindow(t, timelineFirstWindow, transcript.path, source, before, limit)
		samePage(t, fmt.Sprintf("%s before=%d limit=%d", source, before, limit), got, want)
		if next := got.NextBefore; next > 0 && limit > 0 {
			older := readPageWithFirstWindow(t, timelineFirstWindow, transcript.path, source, next, limit)
			samePage(t, fmt.Sprintf("%s older page before=%d limit=%d", source, next, limit), older, readPageWithFirstWindow(t, timelineReadBudget, transcript.path, source, next, limit))
		}
	}
}

func TestATranscriptPageReadsWhatItShowsAndMatchesAReadOfTheWholeBudget(t *testing.T) {
	transcript := writePageTranscript(t, claudePageRecords, 26000, 1)
	checkPagesMatchTheWholeBudgetRead(t, transcript, "claude", 24)
}

func TestACodexTranscriptPageMatchesAReadOfTheWholeBudget(t *testing.T) {
	transcript := writePageTranscript(t, codexPageRecords, 20000, 2)
	checkPagesMatchTheWholeBudgetRead(t, transcript, "codex", 16)
}

func TestAPageWhoseToolOutputIsLargerThanTheFirstWindowStillShowsIt(t *testing.T) {
	huge := func(turn int, rng *rand.Rand) []map[string]any {
		records := claudePageRecords(turn, rng)
		if turn == 30 || turn == 60 {
			at := records[0]["timestamp"]
			id := fmt.Sprintf("huge-%d", turn)
			records = append(records[:1], append([]map[string]any{
				{"type": "assistant", "uuid": "tool-" + id, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "cat log"}}}}},
				{"type": "user", "uuid": "result-" + id, "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("x", int(3*timelineFirstWindow))}}}},
			}, records[1:]...)...)
		}
		return records
	}
	transcript := writePageTranscript(t, huge, 60, 3)
	for _, limit := range []int{1, 2, 10} {
		shown := 0
		for before, pages := int64(0), 0; pages < 100; pages++ {
			got := readPageWithFirstWindow(t, timelineFirstWindow, transcript.path, "claude", before, limit)
			samePage(t, fmt.Sprintf("limit=%d before=%d", limit, before), got, readPageWithFirstWindow(t, timelineReadBudget, transcript.path, "claude", before, limit))
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
			output = strings.Repeat("x", 2*timelineTextBudget)
		}
		return append(records,
			map[string]any{"type": "assistant", "uuid": "tool-" + id, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "content": []map[string]any{{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": "go test"}}}}},
			map[string]any{"type": "user", "uuid": "result-" + id, "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": output}}}},
			map[string]any{"type": "assistant", "uuid": fmt.Sprintf("answer-%d", turn), "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("answer %d", turn)}}}},
		)
	}
	transcript := writePageTranscript(t, older, 3, 4)
	if newest := readPageWithFirstWindow(t, timelineFirstWindow, transcript.path, "claude", 0, 1); newest.Truncated {
		t.Fatal("the newest exchange is marked truncated by an older output it does not show")
	}
	if whole := readPageWithFirstWindow(t, timelineFirstWindow, transcript.path, "claude", 0, 3); !whole.Truncated {
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
			return append(records, map[string]any{"type": "user", "uuid": fmt.Sprintf("result-filler-%d", turn), "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": "none", "content": strings.Repeat("f", int(timelineFirstWindow)/8)}}}})
		}
		return records
	}
	transcript := writePageTranscript(t, quiet, 2040, 5)
	for _, limit := range []int{1, 2, 10} {
		got := readPageWithFirstWindow(t, timelineFirstWindow, transcript.path, "claude", 0, limit)
		samePage(t, fmt.Sprintf("limit=%d", limit), got, readPageWithFirstWindow(t, timelineReadBudget, transcript.path, "claude", 0, limit))
		if got.Reply == nil || got.Reply.Text != "answer 2000" {
			t.Fatalf("limit=%d: reply=%+v, want the last answer, from turn 2000", limit, got.Reply)
		}
	}
}
