package daemon

import (
	"os"
	"strings"
	"testing"
)

func TestAClaudeTranscriptThatGoesMissingIsReportedAsMissingOnEverySeed(t *testing.T) {
	claude, session, r, path := newHostedSourceFixture(t)
	manager := newTestSourceManager(t, r)
	t.Cleanup(manager.shutdown)
	writeFirstTurn(t, path)
	if err := detachedSource(manager, session, claude).loadSeed(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		err := detachedSource(manager, session, claude).seedJournal()
		if err == nil || !strings.Contains(err.Error(), "claude transcript is unavailable") || strings.Contains(err.Error(), "Codex") {
			t.Fatalf("seed %d of a Claude session whose transcript is gone: err=%v", attempt, err)
		}
	}
}
