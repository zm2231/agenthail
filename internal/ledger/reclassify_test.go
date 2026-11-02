package ledger_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAReclassifiedHookCaptureLeavesThePersonsWords(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewCachedStore(db, db)
	if err := s.Enable("p"); err != nil {
		t.Fatal(err)
	}
	for _, u := range []ledger.Utterance{said("claude-prompt:wake", "check the review"), said("claude-prompt:typed", "ship it")} {
		if _, _, err := s.RecordUtterance(u); err != nil {
			t.Fatal(err)
		}
	}
	delivered := said(ledger.DeliveryRef("d1"), "from the app")
	delivered.CapturedBy = ledger.CapturedByDelivery
	if _, _, err := s.RecordUtterance(delivered); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if done, err := s.ReclassifyUtterance("p", "claude-prompt:wake", "scheduled", now); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	if done, err := s.ReclassifyUtterance("p", "claude-prompt:wake", "scheduled", now); err != nil || done {
		t.Fatalf("a second reclassify wrote again: %v %v", done, err)
	}
	if done, err := s.ReclassifyUtterance("p", ledger.DeliveryRef("d1"), "scheduled", now); err != nil || done {
		t.Fatalf("a delivery capture is the person's by construction: %v %v", done, err)
	}

	unattached, err := s.UnattachedFor("p", "s1")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, u := range unattached {
		ids = append(ids, u.ID)
	}
	if strings.Join(ids, ",") != "claude-prompt:typed,"+ledger.DeliveryRef("d1") {
		t.Fatalf("unattached after reclassify: %v", ids)
	}
	_, err = s.Attach(ledger.AttachRequest{ProjectKey: "p", SessionID: "s1", UtteranceID: "claude-prompt:wake", As: ledger.AsConversation})
	if err == nil || !strings.Contains(err.Error(), "not a captured person utterance") {
		t.Fatalf("attaching reclassified words: %v", err)
	}

	board, err := ledger.NewStore(db, db).Board("p")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range board.Utterances() {
		if u.ID == "claude-prompt:wake" {
			t.Fatal("a rebuilt board still holds the reclassified words")
		}
	}
}
