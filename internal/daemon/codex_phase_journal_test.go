package daemon

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const phasedRolloutTurn = `{"type":"response_item","payload":{"id":"NAME-c","type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"NAME checking"}]}}
{"type":"response_item","payload":{"id":"NAME-f","type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"NAME answer"}]}}
`

func phasedTurn(name string) string {
	return strings.ReplaceAll(phasedRolloutTurn, "NAME", name)
}

func journalCommentary(t *testing.T, reg *registry.Registry, sessionID string) map[string]bool {
	t.Helper()
	page, err := reg.ReadSessionJournalPage(sessionID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	commentary := map[string]bool{}
	for _, entry := range page.Entries {
		event, err := sessionstream.DecodePayload(entry.Seq, entry.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if event.SpokenAssistantContent() {
			commentary[event.Body] = event.Commentary()
		}
	}
	return commentary
}

func TestACodexRolloutsCommentaryReachesTheSessionStreamApartFromItsAnswer(t *testing.T) {
	var finished atomic.Bool
	managedTurnAppServer(t, &finished)
	_, reg, _, _, _ := daemonFixture(t)
	session := managedCodexSession(t, reg, managedRolloutStart+phasedTurn("seeded"))
	manager := newTestSourceManager(t, reg)
	subscription, err := manager.prepareStream(context.Background(), &session, surfaces.NewCodex("1"))
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	awaitJournalBody(t, reg, session.ID, "seeded answer")
	if err := appendFile(t, session.Transcript, phasedTurn("tailed")); err != nil {
		t.Fatal(err)
	}
	awaitJournalBody(t, reg, session.ID, "tailed answer")
	deadline := time.Now().Add(time.Second)
	for {
		got := journalCommentary(t, reg, session.ID)
		want := map[string]bool{"seeded checking": true, "seeded answer": false, "tailed checking": true, "tailed answer": false}
		matched := len(got) == len(want)
		for body, commentary := range want {
			if value, found := got[body]; !found || value != commentary {
				matched = false
			}
		}
		if matched {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("commentary by body=%v want=%v", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
