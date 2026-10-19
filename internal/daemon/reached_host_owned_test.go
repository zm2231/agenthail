package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zm2231/agenthail/internal/signin"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// reachedRemoteID is a conversation that was copied to a paired host and is
// mirrored from there, so the same id names a local transcript and a
// host-owned row.
const reachedRemoteID = "3d1c8e52-6a0f-4b8e-9f7d-2a51c0e4b7a1"

// conversationMovingClaude is the real Claude adapter whose send or compact
// reports the session under the conversation a paired host owns.
type conversationMovingClaude struct {
	*surfaces.Claude
	to string
}

func (c *conversationMovingClaude) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	result, err := c.Claude.Send(ctx, sess, message)
	sess.ID = c.to
	return result, err
}

func (c *conversationMovingClaude) Compact(ctx context.Context, sess *surface.Session) error {
	err := c.Claude.Compact(ctx, sess)
	sess.ID = c.to
	return err
}

func pairForgeOwning(t *testing.T, r *registry.Registry, sessionID string) string {
	t.Helper()
	hostID := "h0123456789ab"
	if _, err := r.AddHost(registry.PairedHost{ID: hostID, Name: "forge", Endpoint: "https://forge.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	entity := hostID + "::" + sessionID
	if _, _, err := r.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: surface.Session{ID: sessionID, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "remote"}, EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `"}`)}); err != nil {
		t.Fatal(err)
	}
	return hostID
}

// newReachedHostOwnedFixture makes the ended local session come back under
// the conversation id a paired host already owns.
func newReachedHostOwnedFixture(t *testing.T) (*orphanFixture, string) {
	t.Helper()
	f := newOrphanFixture(t)
	hostID := pairForgeOwning(t, f.r, reachedRemoteID)
	f.daemon = New(f.r, []surface.Surface{&conversationMovingClaude{Claude: surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, f.claude.Home, f.posts.request), to: reachedRemoteID}})
	f.handler = dashboardRouter(f.daemon)
	return f, hostID
}

func TestDiscoveringLiveProcessOfHostOwnedConversationLeavesTheHostRow(t *testing.T) {
	fixture := claudetest.New(t)
	r, err := registry.Open(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	hostID := pairForgeOwning(t, r, reachedRemoteID)
	fixture.Transcript(reachedRemoteID, fixture.Home)
	fixture.Record(reachedRemoteID, fixture.Home, "session_copied_bridge")
	d := New(r, []surface.Surface{surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, (&orphanPosts{}).request)})
	d.discoverCatalog(context.Background())
	if owner, err := r.SessionHost(reachedRemoteID); err != nil || owner != hostID {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	if _, found, err := r.RuntimeState(reachedRemoteID); err != nil || found {
		t.Fatalf("runtime state under host-owned id found=%v err=%v", found, err)
	}
	session, err := r.Session(reachedRemoteID)
	if err != nil || session.PID != 0 || session.Transcript != "" || session.Name != "remote" {
		t.Fatalf("host row rewritten by local discovery: %+v err=%v", session, err)
	}
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
