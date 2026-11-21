package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func TestClaudeDurationUsesRecordedMilliseconds(t *testing.T) {
	path := surfacetest.TimelineFixture(t, "{\"type\":\"system\",\"subtype\":\"turn_duration\",\"durationMs\":114536}\n")
	page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Text != "1m55s" {
		t.Fatalf("duration was dropped: %+v", page)
	}
}
