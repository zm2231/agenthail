package registry

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestOnlyTheHostASessionIsRecordedUnderCanHandItToAnother(t *testing.T) {
	r := registryWithHosts(t, arrivalSource, arrivalPeer, arrivalHome)
	mirror(t, r, arrivalSource, moveConversation)
	owner := func() string {
		t.Helper()
		host, err := r.SessionHost(moveConversation)
		if err != nil {
			t.Fatal(err)
		}
		return host
	}

	if handed, err := r.HandOverHostSession(arrivalPeer, moveConversation, arrivalPeer+"::"+moveConversation, arrivalHome); err != nil || handed || owner() != arrivalSource {
		t.Fatalf("a host the session is not recorded under handed it over: handed=%v err=%v owner=%s", handed, err, owner())
	}
	if handed, err := r.HandOverHostSession(arrivalSource, moveConversation, arrivalSource+"::"+moveConversation, "h0000000000ff"); err != nil || handed || owner() != arrivalSource {
		t.Fatalf("the session was handed to a host that is not paired: handed=%v err=%v owner=%s", handed, err, owner())
	}
	var owned SessionOwnedError
	if _, _, err := r.ApplyHostCatalogRow(arrivalPeer, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}, EntityID: arrivalPeer + "::" + moveConversation, Projection: json.RawMessage(`{"id":"` + arrivalPeer + "::" + moveConversation + `"}`)}); !errors.As(err, &owned) || owned.HostID != arrivalSource {
		t.Fatalf("another host's row for the session answered %v, want it refused as %s's", err, arrivalSource)
	}

	if handed, err := r.HandOverHostSession(arrivalSource, moveConversation, arrivalSource+"::"+moveConversation, arrivalPeer); err != nil || !handed || owner() != arrivalPeer {
		t.Fatalf("the owner's handover answered handed=%v err=%v owner=%s", handed, err, owner())
	}
	mirror(t, r, arrivalPeer, moveConversation)
	if _, _, err := r.ApplyHostCatalogRow(arrivalSource, HostCatalogRow{Session: surface.Session{ID: moveConversation, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}, EntityID: arrivalSource + "::" + moveConversation, Projection: json.RawMessage(`{"id":"` + arrivalSource + "::" + moveConversation + `"}`)}); !errors.As(err, &owned) || owned.HostID != arrivalPeer {
		t.Fatalf("the host the session left still serves it: %v", err)
	}
}

func TestAHostListsTheSessionsItMovedOutWithWhereTheyWent(t *testing.T) {
	r := registryWithHosts(t, arrivalPeer)
	identity, err := r.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	const moved, kept = "moved-out", "kept-here"
	for _, id := range []string{moved, kept} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 9, Transcript: "/tmp/" + id + ".jsonl", HasLocal: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.BeginMoveOut("move-out", moved, arrivalPeer, []byte(`{}`), MoveCarry{}, identity.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.SetMoveState("move-out", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if listed, err := r.MovedOutSessions(); err != nil || len(listed) != 0 {
		t.Fatalf("a move not yet flipped is listed: %+v err=%v", listed, err)
	}
	entity := arrivalPeer + "::" + moved
	if _, err := r.CompleteMoveOut("move-out", MovePlaceholder{EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `","surface":"claude"}`), UnavailableReason: "moved"}); err != nil {
		t.Fatal(err)
	}
	listed, err := r.MovedOutSessions()
	if err != nil || !slices.Equal(listed, []MovedSession{{SessionID: moved, MovedToHost: arrivalPeer}}) {
		t.Fatalf("the sessions moved out are %+v err=%v, want %s on %s", listed, err, moved, arrivalPeer)
	}
}

func TestSessionsHeldForAHostSilentPastTheThresholdAreNamed(t *testing.T) {
	r := registryWithHosts(t, arrivalSource, arrivalPeer)
	mirror(t, r, arrivalSource, "on-silent")
	mirror(t, r, arrivalPeer, "on-recent")
	now := time.Now()
	for host, contact := range map[string]time.Time{arrivalSource: now.Add(-48 * time.Hour), arrivalPeer: now.Add(-time.Hour)} {
		if _, err := r.db.Exec(`UPDATE hosts SET state='disconnected',last_contact_at=? WHERE host_id=?`, formatTimestamp(contact), host); err != nil {
			t.Fatal(err)
		}
	}
	silent, err := r.SilentHostSessions(24*time.Hour, now)
	if err != nil || len(silent) != 1 || silent[0].HostID != arrivalSource || !slices.Equal(silent[0].Sessions, []string{"on-silent"}) {
		t.Fatalf("silent hosts are %+v err=%v, want only %s holding on-silent", silent, err, arrivalSource)
	}
	if _, err := r.db.Exec(`UPDATE hosts SET state='connected' WHERE host_id=?`, arrivalSource); err != nil {
		t.Fatal(err)
	}
	if silent, err := r.SilentHostSessions(24*time.Hour, now); err != nil || len(silent) != 0 {
		t.Fatalf("a connected host is named silent: %+v err=%v", silent, err)
	}
}
