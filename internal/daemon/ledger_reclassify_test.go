package daemon

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestWorkAttachedFromAScheduledPromptIsTakenBackWithOneNotice(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-raced", "raced-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	s := p.session(t, r, "racer")
	ref := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "wake-r", Prompt: "Add filters and continue."}).Ref
	var goal ledgerGoalResponse
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: p.key, SessionID: s.ID, Name: "Search"}, &goal); code != http.StatusOK {
		t.Fatalf("goal %d %s", code, body)
	}
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: s.ID, UtteranceID: ref, As: ledger.AsNewAsk, To: goal.Goal.ID, Clauses: []string{"add filters"}}, nil); code != http.StatusOK {
		t.Fatalf("attach %d %s", code, body)
	}
	source := journalSource(d, s, nil)
	scheduled := liveEvent(surface.TimelineItem{ID: "event-wake-r", Kind: "event", Title: "Claude Code instructions", PromptID: "wake-r", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)})
	source.append(scheduled)
	source.append(scheduled)

	board, err := p.store.Board(p.key)
	if err != nil {
		t.Fatal(err)
	}
	if asks := board.AllAsks(); len(asks) != 0 {
		t.Fatalf("an ask built from a scheduled prompt survived: %+v", asks)
	}
	var notices []string
	rows, err := r.ReadDB().Query(`SELECT message FROM message_queue WHERE session_id=? AND delivery_key=?`, s.ID, "ledger:retracted:"+ref+":"+s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			t.Fatal(err)
		}
		notices = append(notices, message)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "retracted: ask") || !strings.Contains(notices[0], "Redo any of it only from the person's own words") {
		t.Fatalf("notices = %q", notices)
	}
}

func TestAScheduledPromptIsNotThePersonsWordsInEitherOrder(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-wake", "wake-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	s := p.session(t, r, "looping")
	source := journalSource(d, s, nil)
	now := time.Now()
	scheduled := func(promptID string) surface.TimelineItem {
		return surface.TimelineItem{ID: "event-" + promptID, Kind: "event", Title: "Claude Code instructions", PromptID: promptID, Timestamp: now.UTC().Format(time.RFC3339Nano)}
	}

	if !ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "wake-1", Prompt: "Check the verdict; continue the loop."}).Captured {
		t.Fatal("the hook capture should land before the journal says otherwise")
	}
	source.append(liveEvent(scheduled("wake-1")))

	source.append(liveEvent(scheduled("wake-2")))
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "wake-2", Prompt: "Check again."}).Captured {
		t.Fatal("a prompt the journal already named as scheduled was captured")
	}

	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "typed-1", Prompt: "ship it"})
	source.append(liveEvent(typedPrompt("typed-1", "typed-1", "ship it", now)))

	unattached, err := p.store.ProvisionalFor(p.key, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unattached) != 1 || unattached[0].ID != ledger.PromptRef("typed-1") {
		t.Fatalf("person words after a scheduled prompt: %+v", unattached)
	}
	var reclassified int
	for _, e := range p.events(t) {
		if e.Type == ledger.EventReclassified {
			reclassified++
		}
	}
	if reclassified != 1 {
		t.Fatalf("want one reclassification, got %d", reclassified)
	}
}
