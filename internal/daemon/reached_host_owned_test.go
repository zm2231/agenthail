package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const reachedRemoteID = "session_remote_owned"

// newReachedHostOwnedFixture makes the ended local session resume into a
// conversation id a paired host already owns.
func newReachedHostOwnedFixture(t *testing.T) (*orphanFixture, string) {
	t.Helper()
	f := newOrphanFixture(t)
	hostID := "h0123456789ab"
	if _, err := f.r.AddHost(registry.PairedHost{ID: hostID, Name: "forge", Endpoint: "https://forge.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::" + reachedRemoteID
	if _, _, err := f.r.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: surface.Session{ID: reachedRemoteID, Surface: surface.KindClaude, Name: "remote"}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	f.claude.ResumeBridge(reachedRemoteID)
	return f, hostID
}

func assertNothingRecordedUnder(t *testing.T, f *orphanFixture, hostID string) {
	t.Helper()
	if owner, err := f.r.SessionHost(reachedRemoteID); err != nil || owner != hostID {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	if _, found, err := f.r.RuntimeState(reachedRemoteID); err != nil || found {
		t.Fatalf("runtime state under host-owned id found=%v err=%v", found, err)
	}
	history, err := f.r.ListHistory(100, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if entry.SessionID == reachedRemoteID {
			t.Fatalf("history under host-owned id: %+v", entry)
		}
	}
}

func TestSendResumingIntoHostOwnedSessionIsUnknownAndRecordsNothingThere(t *testing.T) {
	f, hostID := newReachedHostOwnedFixture(t)
	code, body := f.send(t, "owned-1", "hello")
	encoded, _ := json.Marshal(body)
	if code == http.StatusOK || !strings.Contains(string(encoded), "forge") || !strings.Contains(string(encoded), "unknown") {
		t.Fatalf("status=%d body=%s", code, encoded)
	}
	if f.posts.count("hello") != 1 {
		t.Fatalf("provider posts=%d", f.posts.count("hello"))
	}
	assertNothingRecordedUnder(t, f, hostID)
}

func TestQueuedSendResumingIntoHostOwnedSessionIsUnknownAndNotRetried(t *testing.T) {
	f, hostID := newReachedHostOwnedFixture(t)
	id, err := f.r.QueueMessageWithKey(f.session.ID, "queued hello", "owned:queued")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		f.daemon.scanAndRelay(context.Background())
	}
	item, err := f.r.QueueItem(id)
	if err != nil || item.Status != "dead" || !strings.Contains(item.LastError, "forge") {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if f.posts.count("queued hello") != 1 {
		t.Fatalf("provider posts=%d", f.posts.count("queued hello"))
	}
	assertNothingRecordedUnder(t, f, hostID)
}

func TestQueuedCompactResumingIntoHostOwnedSessionIsUnknownAndNotRetried(t *testing.T) {
	f, hostID := newReachedHostOwnedFixture(t)
	f.posts.onPost = func(body string) {
		var envelope struct {
			Events []struct {
				Payload struct {
					UUID string `json:"uuid"`
				} `json:"payload"`
			} `json:"events"`
		}
		if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Events) != 1 {
			return
		}
		transcript, err := os.OpenFile(f.session.Transcript, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer transcript.Close()
		fmt.Fprintf(transcript, "{\"type\":\"user\",\"uuid\":%q,\"message\":{\"content\":\"/compact\"}}\n{\"type\":\"system\",\"subtype\":\"compact_boundary\",\"content\":\"done\"}\n", envelope.Events[0].Payload.UUID)
	}
	id, err := f.r.QueueCompact(f.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.daemon.scanAndRelay(context.Background())
	var item *registry.QueueRow
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if item, err = f.r.QueueItem(id); err == nil && item.Status == "dead" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.daemon.scanAndRelay(context.Background())
	if err != nil || item.Status != "dead" || !strings.Contains(item.LastError, "forge") {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if f.resumes() != 1 {
		t.Fatalf("resumes=%d", f.resumes())
	}
	assertNothingRecordedUnder(t, f, hostID)
}
