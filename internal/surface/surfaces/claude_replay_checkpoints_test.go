package surfaces

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func checkpointTurnLines(turn string, base int, returning bool) []string {
	assistant := func(at int, id string, tokens int, end bool) string {
		stop := ""
		if end {
			stop = `"stop_reason":"end_turn",`
		}
		return fmt.Sprintf(`{"type":"assistant","uuid":"a%d","timestamp":"2026-10-20T10:00:%02dZ","message":{"id":"%s","model":"claude-fixture",%s"content":[{"type":"text","text":"step"}],"usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":%d,"output_tokens":%d}}}`, at, at, id, stop, tokens, tokens*10, tokens+1)
	}
	lines := []string{
		`{"type":"user","uuid":"u0","timestamp":"2026-10-20T10:00:00Z","message":{"content":"warm up"}}`,
		assistant(1, "m0", base, true),
		`{"type":"system","subtype":"turn_duration","timestamp":"2026-10-20T10:00:02Z","durationMs":2000}`,
		`{"type":"user","uuid":"` + turn + `","timestamp":"2026-10-20T10:00:03Z","message":{"content":"run it"}}`,
	}
	for step := 1; step <= 6; step++ {
		lines = append(lines, assistant(3+step, fmt.Sprintf("m%d", step), base+step, false))
	}
	if returning {
		lines = append(lines, assistant(10, "m1", base+50, false))
	}
	return append(lines, assistant(11, "m7", base+7, true), `{"type":"system","subtype":"turn_duration","timestamp":"2026-10-20T10:00:12Z","durationMs":9000}`)
}

func checkpointLineOffset(lines []string, index int) int64 {
	var offset int64
	for _, line := range lines[:index] {
		offset += int64(len(line)) + 1
	}
	return offset
}

func checkpointTurnRead(t *testing.T, path string, from int64) map[string]*surface.TurnMetadata {
	t.Helper()
	read, err := NewClaude(nil, t.TempDir()).TranscriptItems(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, surface.TranscriptItemsRequest{From: from})
	if err != nil {
		t.Fatal(err)
	}
	turns := map[string]*surface.TurnMetadata{}
	for _, item := range read.Items {
		if item.Kind == "done" {
			turns[item.TurnID] = item.Turn
		}
	}
	return turns
}

func withReplayCheckpointsEveryRecord(t *testing.T) {
	spacing := replayCheckpointSpacing
	replayCheckpointSpacing = 1
	t.Cleanup(func() { replayCheckpointSpacing = spacing })
}

func TestAMessageReturningAfterACheckpointIsCountedOnce(t *testing.T) {
	withReplayCheckpointsEveryRecord(t)
	lines := checkpointTurnLines("u1", 10, true)
	path := writeTranscript(t, strings.Join(lines, "\n")+"\n")
	whole := checkpointTurnRead(t, path, 0)["u1"]
	if whole == nil || whole.Usage == nil {
		t.Fatalf("a read from the start ended no turn u1 with usage: %+v", whole)
	}
	checkpointTurnRead(t, path, checkpointLineOffset(lines, 7))
	after := checkpointTurnRead(t, path, checkpointLineOffset(lines, len(lines)-2))["u1"]
	if !reflect.DeepEqual(after, whole) {
		t.Fatalf("a read after the returning message counts %+v; a read from the start counts %+v", after.Usage, whole.Usage)
	}
}

func TestATranscriptRewrittenInPlaceIsNotReadFromItsOldCheckpoints(t *testing.T) {
	withReplayCheckpointsEveryRecord(t)
	before := checkpointTurnLines("u1", 10, false)
	shortened := slices.Clone(before)
	shortened[6] = strings.Replace(before[6], `"input_tokens":13,`, `"input_tokens":93,`, 1)
	shortened[9] = strings.Replace(before[9], `"text":"step"`, `"text":"s"`, 1)
	for _, rewrite := range []struct {
		name, turn string
		lines      []string
	}{
		{"with a new turn at the same offsets", "v1", checkpointTurnLines("v1", 20, false)},
		{"with an earlier record changed and the file shortened", "u1", shortened},
	} {
		t.Run(rewrite.name, func(t *testing.T) {
			path := writeTranscript(t, strings.Join(before, "\n")+"\n")
			from := checkpointLineOffset(before, 8)
			if from != checkpointLineOffset(rewrite.lines, 8) {
				t.Fatal("the rewrite must keep its records at the same offsets")
			}
			checkpointTurnRead(t, path, checkpointLineOffset(before, len(before)))
			if err := os.WriteFile(path, []byte(strings.Join(rewrite.lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			whole, after := checkpointTurnRead(t, path, 0), checkpointTurnRead(t, path, from)
			if after[rewrite.turn] == nil || !reflect.DeepEqual(after[rewrite.turn], whole[rewrite.turn]) {
				t.Fatalf("read from offset %d it ends %v; read from the start it ends turn %s with %+v", from, after, rewrite.turn, whole[rewrite.turn])
			}
		})
	}
}
