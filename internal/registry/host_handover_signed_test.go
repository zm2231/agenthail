package registry

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

type signedMoveHosts struct {
	a, b, c  *Registry
	aID, bID string
}

func pairedWith(t *testing.T, r *Registry, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := r.AddHost(PairedHost{ID: id, Name: "host-" + id, Endpoint: "https://" + id + ".example.ts.net:7412", Address: "100.64.0.9", Token: "ahd_" + id}); err != nil {
			t.Fatal(err)
		}
	}
}

func hostID(t *testing.T, r *Registry) string {
	t.Helper()
	identity, err := r.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return identity.ID
}

func signedMoveMesh(t *testing.T) signedMoveHosts {
	t.Helper()
	m := signedMoveHosts{a: openTestRegistry(t), b: openTestRegistry(t)}
	m.aID, m.bID = hostID(t, m.a), hostID(t, m.b)
	pairedWith(t, m.a, m.bID)
	pairedWith(t, m.b, m.aID)
	m.c = registryWithHosts(t, m.aID, m.bID)
	mirror(t, m.c, m.aID, moveConversation)
	if err := m.a.RecordObservedSession(surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 7, Transcript: "/tmp/t.jsonl", HasLocal: true}); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"handoverSeq":1}`)
	if err := m.a.BeginMoveOut("move-1", moveConversation, m.bID, manifest, MoveCarry{}, m.aID); err != nil {
		t.Fatal(err)
	}
	if err := m.a.SetMoveState("move-1", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := m.b.PrepareMoveIn("move-1", moveConversation, m.aID, manifest); err != nil {
		t.Fatal(err)
	}
	if err := m.b.SetMoveState("move-1", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	keys, err := m.a.HostKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.c.RecordPeerHostKeys(m.aID, keys); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m signedMoveHosts) resumeOnB(t *testing.T) {
	t.Helper()
	session := surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 8, Transcript: "/tmp/b.jsonl", HasLocal: true}
	if _, err := m.b.TakeMovedSession("move-1", session, "", time.Time{}, nil, MoveCarry{}, m.bID, MovedCoordinatorPlace{}); err != nil {
		t.Fatal(err)
	}
}

func (m signedMoveHosts) flipOnA(t *testing.T) hosts.Handover {
	t.Helper()
	entity := m.bID + "::" + moveConversation
	if _, err := m.a.CompleteMoveOut("move-1", MovePlaceholder{EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `","surface":"claude"}`), UnavailableReason: "moved"}); err != nil {
		t.Fatal(err)
	}
	signed, err := m.a.UndeliveredHandovers(m.bID)
	if err != nil || len(signed) != 1 {
		t.Fatalf("the finished move left handovers %+v err=%v, want one", signed, err)
	}
	return signed[0]
}

func (m signedMoveHosts) owner(t *testing.T) string {
	t.Helper()
	owner, err := m.c.SessionHost(moveConversation)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestAThirdHostTakesAMovedSessionOnTheHandoverItsNewHostServes(t *testing.T) {
	m := signedMoveMesh(t)
	m.resumeOnB(t)
	signed := m.flipOnA(t)
	if err := m.b.KeepHandover(signed, m.aID, m.bID); err != nil {
		t.Fatal(err)
	}
	served, err := m.b.ServedHandovers(moveConversation)
	if err != nil || len(served) != 1 || served[0].Seq != 1 || served[0].From != m.aID || served[0].To != m.bID {
		t.Fatalf("the new host serves %+v err=%v", served, err)
	}
	if err := m.a.MarkHandoverDelivered(signed.MoveID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if left, err := m.a.UndeliveredHandovers(m.bID); err != nil || len(left) != 0 {
		t.Fatalf("a delivered handover is still due: %+v err=%v", left, err)
	}

	_, handed, err := m.c.HandOverSigned(served, moveConversation, m.bID)
	if err != nil || !handed || m.owner(t) != m.bID {
		t.Fatalf("the signed handover answered handed=%v err=%v owner=%s", handed, err, m.owner(t))
	}
	if seq, err := m.c.HandoverSeq(moveConversation); err != nil || seq != 1 {
		t.Fatalf("the third host counts %d handovers err=%v, want 1", seq, err)
	}
	if seq, err := m.b.HandoverSeq(moveConversation); err != nil || seq != 1 {
		t.Fatalf("the new host counts %d handovers err=%v, want 1", seq, err)
	}
}

func TestAThirdHostRefusesAHandoverThatIsForgedReplayedOrUnderARevokedKey(t *testing.T) {
	refused := func(t *testing.T, m signedMoveHosts, handover hosts.Handover, servedBy string) {
		t.Helper()
		_, handed, err := m.c.HandOverSigned([]hosts.Handover{handover}, moveConversation, servedBy)
		if !errors.As(err, &HandoverRefusedError{}) || handed || m.owner(t) != m.aID {
			t.Fatalf("the handover answered handed=%v err=%v owner=%s, want it refused and the session still %s's", handed, err, m.owner(t), m.aID)
		}
	}
	setup := func(t *testing.T) (signedMoveHosts, hosts.Handover) {
		m := signedMoveMesh(t)
		m.resumeOnB(t)
		return m, m.flipOnA(t)
	}

	t.Run("a signature that does not match", func(t *testing.T) {
		m, signed := setup(t)
		signed.Signature = append([]byte(nil), signed.Signature...)
		signed.Signature[0] ^= 1
		refused(t, m, signed, m.bID)
	})
	t.Run("a statement changed after signing", func(t *testing.T) {
		m, signed := setup(t)
		signed.Seq = 7
		refused(t, m, signed, m.bID)
	})
	t.Run("served by a host it does not name", func(t *testing.T) {
		m, signed := setup(t)
		refused(t, m, signed, arrivalHome)
	})
	t.Run("an older handover replayed after the session came back", func(t *testing.T) {
		m, signed := setup(t)
		entity := m.aID + "::" + moveConversation
		if handed, err := m.c.HandOverHostSession(m.aID, moveConversation, entity, m.bID, 1); err != nil || !handed {
			t.Fatalf("handed=%v err=%v", handed, err)
		}
		if handed, err := m.c.HandOverHostSession(m.bID, moveConversation, m.bID+"::"+moveConversation, m.aID, 2); err != nil || !handed {
			t.Fatalf("handed=%v err=%v", handed, err)
		}
		refused(t, m, signed, m.bID)
	})
	t.Run("a key its host revoked", func(t *testing.T) {
		m, signed := setup(t)
		if _, _, err := m.a.RotateHostKey(true, time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		keys, err := m.a.HostKeys()
		if err != nil {
			t.Fatal(err)
		}
		if err := m.c.RecordPeerHostKeys(m.aID, keys); err != nil {
			t.Fatal(err)
		}
		refused(t, m, signed, m.bID)
	})
	t.Run("a key its host never published", func(t *testing.T) {
		m, signed := setup(t)
		if err := m.c.RecordPeerHostKeys(m.aID, nil); err != nil {
			t.Fatal(err)
		}
		refused(t, m, signed, m.bID)
	})
}

func TestAHandoverSignedBeforeItsHostRotatedKeysStillHandsTheSessionOver(t *testing.T) {
	m := signedMoveMesh(t)
	m.resumeOnB(t)
	signed := m.flipOnA(t)
	if _, _, err := m.a.RotateHostKey(false, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	keys, err := m.a.HostKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.c.RecordPeerHostKeys(m.aID, keys); err != nil {
		t.Fatal(err)
	}
	if _, handed, err := m.c.HandOverSigned([]hosts.Handover{signed}, moveConversation, m.bID); err != nil || !handed || m.owner(t) != m.bID {
		t.Fatalf("the handover under the retired key answered handed=%v err=%v owner=%s", handed, err, m.owner(t))
	}
}

func TestAMoveAbortedAfterItsSourceSignedNeverServesAHandover(t *testing.T) {
	m := signedMoveMesh(t)
	signed := m.flipOnA(t)
	if err := m.b.KeepHandover(signed, m.aID, m.bID); !errors.As(err, &MoveConflictError{}) {
		t.Fatalf("a move not yet resumed took a handover: %v", err)
	}
	if _, err := m.b.AbortMoveIn("move-1", moveConversation, m.aID); err != nil {
		t.Fatal(err)
	}
	if err := m.b.KeepHandover(signed, m.aID, m.bID); !errors.As(err, &MoveConflictError{}) {
		t.Fatalf("an aborted move took a handover: %v", err)
	}
	if served, err := m.b.ServedHandovers(moveConversation); err != nil || len(served) != 0 {
		t.Fatalf("an aborted move serves %+v err=%v", served, err)
	}
	if err := m.a.SetMoveState("move-1", MoveFailed, "the host refused the move", MoveSent, MovePreparing); !errors.As(err, &MoveConflictError{}) {
		t.Fatalf("the source took back a session it signed away: %v", err)
	}
	if owner, err := m.a.SessionHost(moveConversation); err != nil || owner != m.bID {
		t.Fatalf("the source records the session under %q err=%v, want %s", owner, err, m.bID)
	}
}

func TestAResumedMoveCannotBeAbortedSoItsHandoverStands(t *testing.T) {
	m := signedMoveMesh(t)
	m.resumeOnB(t)
	if _, err := m.b.AbortMoveIn("move-1", moveConversation, m.aID); !errors.As(err, &MoveConflictError{}) {
		t.Fatalf("a resumed move was aborted: %v", err)
	}
	signed := m.flipOnA(t)
	if err := m.b.KeepHandover(signed, m.aID, m.bID); err != nil {
		t.Fatal(err)
	}
	if served, err := m.b.ServedHandovers(moveConversation); err != nil || len(served) != 1 {
		t.Fatalf("the resumed move serves %+v err=%v, want its handover", served, err)
	}
}

func twoHopMesh(t *testing.T) (signedMoveHosts, string, []hosts.Handover) {
	t.Helper()
	m := signedMoveMesh(t)
	m.resumeOnB(t)
	if err := m.b.KeepHandover(m.flipOnA(t), m.aID, m.bID); err != nil {
		t.Fatal(err)
	}
	d := openTestRegistry(t)
	dID := hostID(t, d)
	pairedWith(t, m.b, dID)
	pairedWith(t, d, m.bID)
	pairedWith(t, m.c, dID)
	chain, err := m.b.ServedHandovers(moveConversation)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"handoverSeq": 2, "handovers": chain})
	if err := m.b.BeginMoveOut("move-2", moveConversation, dID, manifest, MoveCarry{}, m.bID); err != nil {
		t.Fatal(err)
	}
	if err := m.b.SetMoveState("move-2", MoveSent, "", MovePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PrepareMoveIn("move-2", moveConversation, m.bID, manifest); err != nil {
		t.Fatal(err)
	}
	if err := d.SetMoveState("move-2", MoveReceived, "", MovePrepared); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TakeMovedSession("move-2", surface.Session{ID: moveConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: 9, Transcript: "/tmp/d.jsonl", HasLocal: true}, "", time.Time{}, nil, MoveCarry{}, dID, MovedCoordinatorPlace{}); err != nil {
		t.Fatal(err)
	}
	entity := dID + "::" + moveConversation
	if _, err := m.b.CompleteMoveOut("move-2", MovePlaceholder{EntityID: entity, Projection: json.RawMessage(`{"id":"` + entity + `","surface":"claude"}`), UnavailableReason: "moved"}); err != nil {
		t.Fatal(err)
	}
	signed, err := m.b.UndeliveredHandovers(dID)
	if err != nil || len(signed) != 1 || signed[0].Seq != 2 {
		t.Fatalf("the second move left handovers %+v err=%v, want one at 2", signed, err)
	}
	if err := d.KeepHandover(signed[0], m.bID, dID); err != nil {
		t.Fatal(err)
	}
	served, err := d.ServedHandovers(moveConversation)
	if err != nil || len(served) != 2 {
		t.Fatalf("the latest host serves %+v err=%v, want both handovers", served, err)
	}
	bKeys, err := m.b.HostKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.c.RecordPeerHostKeys(m.bID, bKeys); err != nil {
		t.Fatal(err)
	}
	return m, dID, served
}

func TestAThirdHostThatMissedEveryMoveFollowsTheSignedChainToTheLatestHost(t *testing.T) {
	m, dID, served := twoHopMesh(t)
	if _, handed, err := m.c.HandOverSigned(served[1:], moveConversation, dID); !errors.As(err, &HandoverRefusedError{}) || handed || m.owner(t) != m.aID {
		t.Fatalf("a chain missing the first move answered handed=%v err=%v owner=%s", handed, err, m.owner(t))
	}
	if _, handed, err := m.c.HandOverSigned(served, moveConversation, dID); err != nil || !handed || m.owner(t) != dID {
		t.Fatalf("the chain answered handed=%v err=%v owner=%s, want the session on the latest host", handed, err, m.owner(t))
	}
	if seq, err := m.c.HandoverSeq(moveConversation); err != nil || seq != 2 {
		t.Fatalf("the third host counts %d handovers err=%v, want 2", seq, err)
	}
}

func TestASignedChainHandsOverFromTheOwnerHeldWhenItIsApplied(t *testing.T) {
	m, dID, served := twoHopMesh(t)
	if handed, err := m.c.HandOverHostSession(m.aID, moveConversation, m.aID+"::"+moveConversation, m.bID, 1); err != nil || !handed {
		t.Fatalf("handed=%v err=%v", handed, err)
	}
	mirror(t, m.c, m.bID, moveConversation)
	before, err := m.c.CatalogEventsAfter(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	from, handed, err := m.c.HandOverSigned(served, moveConversation, dID)
	if err != nil || !handed || from != m.bID || m.owner(t) != dID {
		t.Fatalf("the chain answered from=%s handed=%v err=%v owner=%s, want it handed from %s to %s", from, handed, err, m.owner(t), m.bID, dID)
	}
	after, err := m.c.CatalogEventsAfter(before.LatestSeq, 1000)
	if err != nil {
		t.Fatal(err)
	}
	removed := ""
	for _, event := range after.Events {
		if event.Type == "session.removed" {
			removed = event.EntityID
		}
	}
	if removed != m.bID+"::"+moveConversation {
		t.Fatalf("the handover removed %q, want the row of %s, the owner it handed from", removed, m.bID)
	}
}
