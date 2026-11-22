package ledger_test

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAClaimTellsItsHolderToLogAsItGoes(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:1", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"build export"}})
	added := work(t, s, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"})
	if added.Guidance != "" {
		t.Fatalf("adding work carried guidance: %q", added.Guidance)
	}
	claimed := work(t, s, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1"})
	for _, want := range []string{"Log to widget-W1 as you go", "what's left", `agenthail ledger log widget-W1 "..."`} {
		if !strings.Contains(claimed.Guidance, want) {
			t.Fatalf("claim guidance %q lacks %q", claimed.Guidance, want)
		}
	}
	if logged := work(t, s, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", Text: "chose fts5", LogType: "decision"}); logged.Guidance != "" {
		t.Fatalf("a log carried guidance: %q", logged.Guidance)
	}
}
