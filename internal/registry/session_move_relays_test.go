package registry

import (
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func relayedMoveRegistry(t *testing.T) (*Registry, int64) {
	t.Helper()
	r := moveRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "listener", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	id, err := r.AddRelayRoute(moveConversation, "listener", ".*", false, "")
	if err != nil {
		t.Fatal(err)
	}
	return r, id
}

func TestAMoveRefusesARelayTiedToAChannelNamingTheRelay(t *testing.T) {
	r, id := relayedMoveRegistry(t)
	if _, err := r.db.Exec(`INSERT INTO channels(id,name) VALUES('channel-1','builders')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE routes SET channel_id='channel-1' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	reason, err := r.SessionMoveRefusal(moveConversation, true)
	want := "relay #" + strconv.FormatInt(id, 10) + " is tied to a channel; remove it with 'agenthail relay rm " + strconv.FormatInt(id, 10) + "' first"
	if err != nil || reason != want {
		t.Fatalf("refusal=%q err=%v, want %q", reason, err, want)
	}
}

func TestAMoveRefusesARelayWithACompletionItHasNotForwarded(t *testing.T) {
	r, id := relayedMoveRegistry(t)
	if err := r.CaptureRelayCompletions(moveConversation, "", surface.CompletionBatch{Cursor: "c1", Events: []surface.Completion{{ID: "turn-1", Text: "done"}}}); err != nil {
		t.Fatal(err)
	}
	if reason, err := r.SessionMoveRefusal(moveConversation, true); err != nil || !strings.Contains(reason, "relay #"+strconv.FormatInt(id, 10)+" has a completion it has not forwarded yet") {
		t.Fatalf("refusal=%q err=%v", reason, err)
	}
	if reason, _ := r.SessionMoveRefusal(moveConversation, false); !strings.Contains(reason, "active relay") {
		t.Fatalf("a move between accounts, which keeps relays here, gave %q", reason)
	}
}

func TestAMoveIsRefusedWhenItsRelaysChangeAfterItDeclaredThem(t *testing.T) {
	r, id := relayedMoveRegistry(t)
	if reason, _ := r.SessionMoveRefusal(moveConversation, true); reason != "" {
		t.Fatalf("a relay the move carries refused it: %q", reason)
	}
	if err := r.RegisterSession(surface.Session{ID: "second", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(moveConversation, "second", ".*", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), []int64{id}); err == nil || !strings.Contains(err.Error(), "relays from") {
		t.Fatalf("a move that declared one of two relays began: %v", err)
	}
}

func TestARelayCannotBeAddedFromASessionThatIsMoving(t *testing.T) {
	r, id := relayedMoveRegistry(t)
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), []int64{id}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "second", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(moveConversation, "second", ".*", false, ""); err == nil || !strings.Contains(err.Error(), "is moving to another host") {
		t.Fatalf("a relay was added from a moving session: %v", err)
	}
}

func TestACompletedMoveOutLeavesNoRelayFromTheSessionHere(t *testing.T) {
	r, id := relayedMoveRegistry(t)
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), []int64{id}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompleteMoveOut("move-1", testPlaceholder); err != nil {
		t.Fatal(err)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 0 {
		t.Fatalf("routes after the move=%+v err=%v", routes, err)
	}
}
