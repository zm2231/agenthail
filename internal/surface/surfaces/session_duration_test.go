package surfaces

import (
	"context"
	"testing"
)

func TestClaudeDurationUsesRecordedMilliseconds(t *testing.T) {
	path := timelineFixture(t, "{\"type\":\"system\",\"subtype\":\"turn_duration\",\"durationMs\":114536}\n")
	page, err := readTranscriptPage(context.Background(), path, "claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Text != "1m55s" || page.Items[0].Title != "Turn duration" {
		t.Fatalf("duration was dropped: %+v", page)
	}
}
