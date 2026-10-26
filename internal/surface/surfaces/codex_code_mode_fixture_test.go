package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexCodeModeRollout  = "../../../scripts/fixtures/codex-code-mode.jsonl"
	codexCodeModeTimeline = "../../../scripts/fixtures/codex-code-mode-timeline.json"
)

func TestCodexCodeModeFixtureMatchesTheDaemon(t *testing.T) {
	page, err := readTimeline(context.Background(), codexCodeModeRollout, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	served, err := json.MarshalIndent(surface.PresentTimelineItems(page.Items), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	served = append(served, '\n')
	if os.Getenv("AGENTHAIL_WRITE_FIXTURES") != "" {
		if err := os.WriteFile(codexCodeModeTimeline, served, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := os.ReadFile(codexCodeModeTimeline)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(served) {
		t.Fatalf("%s is stale; regenerate it with AGENTHAIL_WRITE_FIXTURES=1", codexCodeModeTimeline)
	}
}
