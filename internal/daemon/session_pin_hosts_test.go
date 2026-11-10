package daemon

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAPinThroughAPairedHostIsHeldThereAndShowsHere(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	mirrored := func() (snapshot, view time.Time) {
		t.Helper()
		for _, row := range sessionsOf(local.snapshot(t), record.ID) {
			if row.ID == qualified {
				snapshot = row.PinnedAt
			}
		}
		state, err := local.daemon.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range state.Sessions {
			if row.ID == qualified {
				view = row.PinnedAt
			}
		}
		return snapshot, view
	}
	act := func(action string) {
		t.Helper()
		if response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": action, "sessionId": qualified}); response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", action, response.Code, response.Body.String())
		}
		remote.daemon.discoverCatalog(context.Background())
	}

	act("session-pin")
	pinned, err := remote.reg.SessionPinnedAt("remote-a")
	if err != nil || pinned.IsZero() {
		t.Fatalf("the owner did not keep the pin: %v %v", pinned, err)
	}
	testkit.Eventually(t, "the mirrored row to show the owner's pin", 10*time.Second, func() bool {
		snapshot, view := mirrored()
		return snapshot.Equal(pinned) && view.Equal(pinned)
	})

	act("session-unpin")
	testkit.Eventually(t, "the mirrored row to drop the pin", 10*time.Second, func() bool {
		snapshot, view := mirrored()
		return snapshot.IsZero() && view.IsZero()
	})
}
