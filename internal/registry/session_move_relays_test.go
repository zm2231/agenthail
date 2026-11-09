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

func movedRelaysOf(t *testing.T, r *Registry) []MovedRelay {
	t.Helper()
	relays, err := r.MovedRelays(moveConversation, "")
	if err != nil {
		t.Fatal(err)
	}
	return relays
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
	r, _ := relayedMoveRegistry(t)
	if reason, _ := r.SessionMoveRefusal(moveConversation, true); reason != "" {
		t.Fatalf("a relay the move carries refused it: %q", reason)
	}
	declared := movedRelaysOf(t, r)
	if err := r.RegisterSession(surface.Session{ID: "second", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(moveConversation, "second", ".*", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), declared, ""); err == nil || !strings.Contains(err.Error(), "relays from") {
		t.Fatalf("a move that declared one of two relays began: %v", err)
	}
}

func TestAMoveRefusesACompletionCapturedAfterItDeclaredItsRelays(t *testing.T) {
	r, _ := relayedMoveRegistry(t)
	declared := movedRelaysOf(t, r)
	if err := r.CaptureRelayCompletions(moveConversation, "", surface.CompletionBatch{Cursor: "c1", Events: []surface.Completion{{ID: "turn-1", Text: "done"}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), declared, ""); err == nil || !strings.Contains(err.Error(), "has a completion it has not forwarded yet") {
		t.Fatalf("a move began with a completion its relay had not forwarded: %v", err)
	}
}

func TestTheRelaysOfAMovingSessionCannotChange(t *testing.T) {
	r, id := relayedMoveRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "second", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), movedRelaysOf(t, r), ""); err != nil {
		t.Fatal(err)
	}
	before, err := r.ListRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions(moveConversation, "", surface.CompletionBatch{Cursor: "c1", Events: []surface.Completion{{ID: "turn-1", Text: "done"}}}); err != nil {
		t.Fatal(err)
	}
	events, err := r.PendingRelayEvents(before[0])
	if err != nil || len(events) != 1 {
		t.Fatalf("pending events=%+v err=%v", events, err)
	}
	changes := map[string]func() error{
		"add":                func() error { _, err := r.AddRelayRoute(moveConversation, "second", ".*", false, ""); return err },
		"remove":             func() error { return r.RemoveRoute(id) },
		"deactivate":         func() error { return r.DeactivateRoute(id) },
		"hand over from it":  func() error { return r.MoveRelayRoutes(moveConversation, "second") },
		"hand over to it":    func() error { return r.MoveRelayRoutes("second", moveConversation) },
		"forward completion": func() error { _, err := r.ConsumeRelay(before[0], events[0], "done", "", ""); return err },
	}
	for name, change := range changes {
		if err := change(); err == nil || !strings.Contains(err.Error(), "is moving to another host") {
			t.Fatalf("%s: a relay of a moving session changed: %v", name, err)
		}
	}
	after, err := r.ListRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || after[0].ID != before[0].ID || after[0].FromSession != before[0].FromSession || after[0].ToSession != before[0].ToSession || after[0].Active != before[0].Active || after[0].EventCursor != before[0].EventCursor {
		t.Fatalf("routes changed while the session moved: %+v, was %+v", after, before)
	}
}

func TestACompletedMoveOutLeavesNoRelayFromTheSessionHere(t *testing.T) {
	r, _ := relayedMoveRegistry(t)
	if err := r.BeginMoveOut("move-1", moveConversation, moveTarget, []byte(`{}`), movedRelaysOf(t, r), ""); err != nil {
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
