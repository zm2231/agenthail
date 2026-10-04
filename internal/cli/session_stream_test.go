package cli

import (
	"testing"

	"github.com/zm2231/agenthail/internal/sessionstream"
)

func TestStreamEventDeltaDeduplicatesJournalUpserts(t *testing.T) {
	bodies := map[string]string{}
	versions := map[string]uint64{}
	first := sessionstream.Event{ItemID: "answer", Version: 1, Body: "hello"}
	second := sessionstream.Event{ItemID: "answer", Version: 2, Body: "hello world"}
	replay := sessionstream.Event{ItemID: "answer", Version: 2, Body: "hello world"}
	if got := streamEventDelta(first, bodies, versions); got != "hello" {
		t.Fatalf("first delta=%q", got)
	}
	if got := streamEventDelta(second, bodies, versions); got != " world" {
		t.Fatalf("upsert delta=%q", got)
	}
	if got := streamEventDelta(replay, bodies, versions); got != "" {
		t.Fatalf("replay delta=%q", got)
	}
}
