package surfacetest

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
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

type pageTranscript struct {
	Path       string
	Boundaries []int64
}

func WritePageTranscript(t *testing.T, records func(turn int, rng *rand.Rand) []map[string]any, turns int, seed int64) pageTranscript {
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
	return pageTranscript{Path: path, Boundaries: boundaries}
}

func ToolOutput(rng *rand.Rand) string {
	window := int(transcript.TimelineFirstWindow)
	switch roll := rng.Intn(1000); {
	case roll == 0:
		return strings.Repeat("o", window*11/8+rng.Intn(4*window))
	case roll < 30:
		return strings.Repeat("o", window/25+rng.Intn(window*2/5))
	default:
		return strings.Repeat("o", rng.Intn(400))
	}
}

func ReadPageWithFirstWindow(t *testing.T, window int64, path string, format transcript.Format, before int64, limit int) *surface.SessionReadResult {
	t.Helper()
	previous := transcript.TimelineFirstWindow
	transcript.TimelineFirstWindow = window
	defer func() { transcript.TimelineFirstWindow = previous }()
	page, err := transcript.ReadPage(context.Background(), path, format, before, limit)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func SamePage(t *testing.T, label string, got, want *surface.SessionReadResult) {
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

func CheckPagesMatchTheWholeBudgetRead(t *testing.T, written pageTranscript, format transcript.Format, source string, samples int) {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	info, err := os.Stat(written.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= transcript.TimelineReadBudget {
		t.Fatalf("fixture is %d bytes; it must exceed the %d byte budget", info.Size(), transcript.TimelineReadBudget)
	}
	limits := []int{1, 2, 3, 5, 10, 20, 40, surface.MaxExchangePage}
	for sample := 0; sample < samples; sample++ {
		before := int64(0)
		if sample > 0 {
			before = written.Boundaries[1+rng.Intn(len(written.Boundaries)-1)]
		}
		limit := limits[rng.Intn(len(limits))]
		want := ReadPageWithFirstWindow(t, transcript.TimelineReadBudget, written.Path, format, before, limit)
		got := ReadPageWithFirstWindow(t, transcript.TimelineFirstWindow, written.Path, format, before, limit)
		SamePage(t, fmt.Sprintf("%s before=%d limit=%d", source, before, limit), got, want)
		if next := got.NextBefore; next > 0 && limit > 0 {
			older := ReadPageWithFirstWindow(t, transcript.TimelineFirstWindow, written.Path, format, next, limit)
			SamePage(t, fmt.Sprintf("%s older page before=%d limit=%d", source, next, limit), older, ReadPageWithFirstWindow(t, transcript.TimelineReadBudget, written.Path, format, next, limit))
		}
	}
}

// ScaleTranscriptReads keeps the doublings from first window to budget while shrinking both.
func ScaleTranscriptReads(t *testing.T) {
	t.Helper()
	budget, window := transcript.TimelineReadBudget, transcript.TimelineFirstWindow
	transcript.TimelineReadBudget, transcript.TimelineFirstWindow = budget/16, window/16
	t.Cleanup(func() { transcript.TimelineReadBudget, transcript.TimelineFirstWindow = budget, window })
}
