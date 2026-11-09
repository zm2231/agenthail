package ledger_test

import (
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestTheProvisionalNoteCountsWithoutAskingForAnything(t *testing.T) {
	if got := ledger.ProvisionalNote(project, 0); got != "" {
		t.Fatalf("nothing provisional = %q, want no note", got)
	}
	if got, want := ledger.ProvisionalNote(project, 1), "[intent ledger] 1 of the person's messages in "+project+" is provisional until attached: agenthail ledger --provisional"; got != want {
		t.Fatalf("one = %q, want %q", got, want)
	}
	if got, want := ledger.ProvisionalNote(project, 3), "[intent ledger] 3 of the person's messages in "+project+" are provisional until attached: agenthail ledger --provisional"; got != want {
		t.Fatalf("three = %q, want %q", got, want)
	}
}
