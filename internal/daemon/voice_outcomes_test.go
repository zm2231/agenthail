package daemon

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/voice"
)

func journalRow(seq uint64, payload string) registry.SessionJournalEntry {
	return registry.SessionJournalEntry{Seq: seq, Position: seq, Payload: []byte(payload)}
}

func TestVoiceTurnOutcomesStopCleanlyAtAHistoryGapOnALaterPage(t *testing.T) {
	newest := []registry.SessionJournalEntry{journalRow(300, `{"itemId":"d1","kind":"done","turnId":"t1"}`)}
	for seq := uint64(299); seq >= 100; seq-- {
		newest = append([]registry.SessionJournalEntry{journalRow(seq, fmt.Sprintf(`{"itemId":"x%d","kind":"text","role":"assistant","turnId":"other","body":"noise"}`, seq))}, newest...)
	}
	var reads []uint64
	outcomes, err := journalTurnOutcomes(func(before uint64) (registry.SessionJournalPage, error) {
		reads = append(reads, before)
		switch before {
		case 0:
			return registry.SessionJournalPage{Entries: newest, NextBefore: 100}, nil
		default:
			return registry.SessionJournalPage{}, &registry.SessionJournalHistoryGapError{Earliest: 250, Latest: 300}
		}
	}, []string{"t1", "t2"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]voice.TurnOutcome{"t1": {Found: true, Done: true}, "t2": {}}
	if !reflect.DeepEqual(outcomes, want) || !reflect.DeepEqual(reads, []uint64{0, 100}) {
		t.Fatalf("outcomes=%+v reads=%v", outcomes, reads)
	}
}

func TestVoiceTurnOutcomesReadNoMoreThanTheirRowBound(t *testing.T) {
	var reads int
	outcomes, err := journalTurnOutcomes(func(before uint64) (registry.SessionJournalPage, error) {
		reads++
		page := registry.SessionJournalPage{NextBefore: 1}
		for i := range 200 {
			page.Entries = append(page.Entries, journalRow(uint64(i+1), `{"itemId":"x","kind":"text","role":"assistant","turnId":"other","body":"noise"}`))
		}
		return page, nil
	}, []string{"t1"})
	if err != nil {
		t.Fatal(err)
	}
	if reads != voiceOutcomeRows/200 || outcomes["t1"].Found {
		t.Fatalf("reads=%d outcomes=%+v", reads, outcomes)
	}
}
