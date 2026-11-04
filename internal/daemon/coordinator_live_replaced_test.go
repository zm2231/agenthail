package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const fixtureWorker = "fixture-worker"

func replacedLiveLead(t *testing.T, f *coordinatorFixture) string {
	t.Helper()
	if code, body := f.send(t, "lead-first", goneCoordinator, "first"); code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	live := f.current(t)
	f.landed(t, live, "first")
	spec, err := f.r.Coordinator("project-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.HandOverCoordinator(spec, live, goneCoordinator, registry.CoordinatorHandover{ChangedBy: "daemon", Reason: "gone: test"}); err != nil {
		t.Fatal(err)
	}
	if err := f.r.RegisterSession(surface.Session{ID: fixtureWorker, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	return live
}

func (f *coordinatorFixture) sendFromWorker(t *testing.T, key, sessionID, message string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"action": "send", "sessionId": sessionID, "message": message, "sourceSessionId": fixtureWorker})
	response := serveV1Action(f.handler, string(body), key, f.token)
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Errorf("status=%d body=%s", response.Code, response.Body.String())
	}
	return response.Code, decoded
}

func TestAPersonsSendToAReplacedLeadThatIsStillLiveReachesThatLead(t *testing.T) {
	f := newCoordinatorFixture(t)
	live := replacedLiveLead(t, f)
	code, body := f.send(t, "to-the-live-one", live, "for the conversation I am looking at")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if taker := receiptField(body, "sessionId"); taker != live || receiptField(body, "movedTo") != "" {
		t.Fatalf("the send was taken by %q, not the live session it addressed %s: %v", taker, live, body)
	}
	f.landed(t, live, "for the conversation I am looking at")
	if f.started(t) != 1 {
		t.Fatalf("a send to a live replaced lead started another conversation: started=%d", f.started(t))
	}
}

func TestAWorkersReplyToALiveReplacedLeadGoesToTheCurrentLeadAndSaysSo(t *testing.T) {
	f := newCoordinatorFixture(t)
	live := replacedLiveLead(t, f)
	code, body := f.sendFromWorker(t, "worker-reply", live, "done with the task")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	current := f.current(t)
	if current == live || receiptField(body, "sessionId") != current || receiptField(body, "movedTo") != "@fixture-lead" {
		t.Fatalf("current=%s live=%s receipt=%v", current, live, body)
	}
	f.landed(t, current, "done with the task")
	for _, turn := range f.host.Turns(t, live) {
		if turn.Text == "done with the task" {
			t.Fatalf("the worker's reply also reached the replaced lead")
		}
	}
}

func TestAPersonsSendToAReplacedLeadWhoseStateCannotBeReadWaitsForThatLead(t *testing.T) {
	f := newCoordinatorFixture(t)
	live := replacedLiveLead(t, f)
	for _, project := range []string{"copy-a", "copy-b"} {
		dir := filepath.Join(f.claude.Root, "projects", project)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, live+".jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, body := f.send(t, "unknown-state", live, "wait for this one")
	if code != http.StatusOK || receiptField(body, "evidence") != string(surface.EvidenceQueued) || receiptField(body, "sessionId") != live {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if f.r.QueueCount(live) != 1 || f.started(t) != 1 {
		t.Fatalf("queued for the addressed lead=%d started=%d", f.r.QueueCount(live), f.started(t))
	}
}

func TestQueuedMessagesForALiveReplacedLeadFollowWhoSentThem(t *testing.T) {
	f := newCoordinatorFixture(t)
	live := replacedLiveLead(t, f)
	if _, _, err := f.r.QueueDeliveryWithIntent(live, "queued by the person", "", surface.SendOptions{Author: surface.AuthorPerson}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.r.QueueDeliveryWithIntent(live, "queued by the worker", "", surface.SendOptions{SourceSessionID: fixtureWorker}); err != nil {
		t.Fatal(err)
	}
	f.landed(t, live, "queued by the person")
	current := goneCoordinator
	for deadline := time.Now().Add(15 * time.Second); current == goneCoordinator && time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		f.daemon.scanAndRelay(context.Background())
		current = f.current(t)
	}
	if current == live || current == goneCoordinator {
		t.Fatalf("the worker's queued message did not start a current lead: current=%s", current)
	}
	f.landed(t, current, "queued by the worker")
	for _, turn := range f.host.Turns(t, live) {
		if turn.Text == "queued by the worker" {
			t.Fatalf("the worker's queued message reached the replaced lead")
		}
	}
	for _, turn := range f.host.Turns(t, current) {
		if turn.Text == "queued by the person" {
			t.Fatalf("the person's queued message moved to the current lead")
		}
	}
}
