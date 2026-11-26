package daemon

import (
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// Codex connects during thread/start, racing its token's binding; a 401 makes it drop the server.
func TestACodexThreadsHandshakeIsNotRefusedWhenItsTokenBindsDuringTheCheck(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	server := mcpServer(t, d)
	thread := surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle}
	if err := r.RecordObservedSession(thread); err != nil {
		t.Fatal(err)
	}
	pending, err := r.IssuePendingCredential(nil, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	d.credentialRead = func() {
		d.credentialRead = nil
		if claimed, err := r.ClaimPendingCredential(pending.Ref, thread.ID, time.Now()); err != nil || !claimed {
			t.Errorf("claimed=%v err=%v", claimed, err)
		}
	}
	bearer := map[string]string{"Authorization": "Bearer " + pending.Token}
	if status, body := postMCP(t, server, bearer); status != http.StatusOK {
		t.Fatalf("a handshake whose token bound while it was checked: status=%d body=%v", status, body)
	}
	if d.credentialRead != nil {
		t.Fatal("the handshake never read its credential")
	}
	if status, body := postMCP(t, server, bearer); status != http.StatusOK {
		t.Fatalf("the bound token's next handshake: status=%d body=%v", status, body)
	}
	if id := r.CredentialSession(pending.Token); id != thread.ID {
		t.Fatalf("the token belongs to %q, want %q", id, thread.ID)
	}
}
