package daemon

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func mirroredOn(t *testing.T, node *moveNode, owner *moveNode, hostID, sessionID string) {
	t.Helper()
	owner.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the session to be mirrored", 15*time.Second, func() bool {
		owner.daemon.discoverCatalog(context.Background())
		host, err := node.reg.SessionHost(sessionID)
		return err == nil && host == hostID
	})
}

func relayFrom(t *testing.T, node *moveNode, from *surface.Session, to string) int64 {
	t.Helper()
	if err := node.reg.PrepareRelaySource(context.Background(), node.claude, from, ""); err != nil {
		t.Fatal(err)
	}
	id, err := node.reg.AddRelayRoute(from.ID, to, ".*", false, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func routesFrom(t *testing.T, node *moveNode, session string) []registry.RouteRow {
	t.Helper()
	routes, err := node.reg.ListRoutes()
	if err != nil {
		t.Fatal(err)
	}
	var from []registry.RouteRow
	for _, route := range routes {
		if route.FromSession == session {
			from = append(from, route)
		}
	}
	return from
}

func takeTurn(t *testing.T, node *moveNode, session, text string) string {
	t.Helper()
	before := len(node.host.Turns(t, session))
	if _, err := claudehost.NewClient(node.host.Home).Send(context.Background(), claudehost.SendRequest{SessionID: session, UUID: uuid.NewString(), Text: text}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the turn", 15*time.Second, func() bool { return len(node.host.Turns(t, session)) == before+1 })
	turns := node.host.Turns(t, session)
	return turns[len(turns)-1].Reply
}

func TestARelayFromAMovedSessionForwardsOnlyItsNextCompletionFromTheNewHost(t *testing.T) {
	source, target, record, _ := movePair(t)
	session := startMovable(t, source)
	before := source.host.Turns(t, session.ID)[0].Reply
	listener := startMovable(t, target)
	mirroredOn(t, source, target, record.ID, listener.ID)
	relayFrom(t, source, session, listener.ID)

	if _, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"}); err != nil {
		t.Fatal(err)
	}
	if left := routesFrom(t, source, session.ID); len(left) != 0 {
		t.Fatalf("the source kept relays from a session it no longer runs: %+v", left)
	}
	carried := routesFrom(t, target, session.ID)
	if len(carried) != 1 || carried[0].ToSession != listener.ID || !carried[0].Active {
		t.Fatalf("the target runs relays %+v, want one to %s", carried, listener.ID)
	}
	moved, err := target.reg.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	reply := takeTurn(t, target, session.ID, "after the move")
	testkit.Eventually(t, "the relay to forward the new completion", 15*time.Second, func() bool {
		target.daemon.syncRelays(context.Background(), target.claude, moved)
		return relayed(t, target, listener.ID, reply) > 0
	})
	target.daemon.syncRelays(context.Background(), target.claude, moved)
	if replayed := relayed(t, target, listener.ID, before); replayed != 0 {
		t.Fatalf("the relay forwarded the completion from before the move %d times", replayed)
	}
	if forwarded := relayed(t, target, listener.ID, reply); forwarded != 1 {
		t.Fatalf("the relay forwarded the new completion %d times", forwarded)
	}
}

// relayed counts the messages node queued for session that carry text.
func relayed(t *testing.T, node *moveNode, session, text string) int {
	t.Helper()
	rows, err := node.reg.ListQueue(true, node.daemon.adapters())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		if row.SessionID == session && strings.Contains(row.Message, text) {
			count++
		}
	}
	return count
}

func TestARelayToAMovedSessionKeepsDeliveringToItsNewHost(t *testing.T) {
	source, _, record, _ := movePair(t)
	session := startMovable(t, source)
	feeder := startMovable(t, source)
	relayFrom(t, source, feeder, session.ID)

	if _, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"}); err != nil {
		t.Fatal(err)
	}
	if kept := routesFrom(t, source, feeder.ID); len(kept) != 1 || kept[0].ToSession != session.ID || !kept[0].Active {
		t.Fatalf("the relay to the moved session is %+v", kept)
	}
	reply := takeTurn(t, source, feeder.ID, "for the moved session")
	testkit.Eventually(t, "the relay to reach the moved session on its new host", 15*time.Second, func() bool {
		source.daemon.syncRelays(context.Background(), source.claude, feeder)
		return relayed(t, source, session.ID, reply) == 1
	})
	if host, _ := source.reg.SessionHost(session.ID); host != record.ID {
		t.Fatalf("the relay's target is %q's here, want the new host's", host)
	}
}

// moveTrio is a source paired with a target and a third host, the target
// paired with the third host too, and the source's checkout movable to the
// target.
func moveTrio(t *testing.T) (source, target, third *moveNode, toTarget, toThird, targetToThird registry.PairedHost) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target, third = newMoveNode(t), newMoveNode(t), newMoveNode(t)
	checkoutsForMove(t, source, target)
	third.serve(t, nil)
	target.serve(t, nil)
	target.start(t, testRoots(third.server))
	targetToThird = target.pair(t, third.federationNode, "third")
	roots := x509.NewCertPool()
	roots.AddCert(target.server.Certificate())
	roots.AddCert(third.server.Certificate())
	source.start(t, roots)
	toTarget = source.pair(t, target.federationNode, "server")
	toThird = source.pair(t, third.federationNode, "third")
	testkit.Eventually(t, "the hosts connected", 10*time.Second, func() bool {
		return source.daemon.hostConnected(toTarget.ID) && source.daemon.hostConnected(toThird.ID) && target.daemon.hostConnected(targetToThird.ID)
	})
	return source, target, third, toTarget, toThird, targetToThird
}

func TestARelayToAThirdHostsSessionMovesWithItsSource(t *testing.T) {
	source, target, third, _, toThird, targetToThird := moveTrio(t)
	session := startMovable(t, source)
	listener := startMovable(t, third)
	mirroredOn(t, source, third, toThird.ID, listener.ID)
	mirroredOn(t, target, third, targetToThird.ID, listener.ID)
	relayFrom(t, source, session, listener.ID)

	if _, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"}); err != nil {
		t.Fatal(err)
	}
	carried := routesFrom(t, target, session.ID)
	if len(carried) != 1 || carried[0].ToSession != listener.ID {
		t.Fatalf("the target runs relays %+v, want one to %s on the third host", carried, listener.ID)
	}
	moved, err := target.reg.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	reply := takeTurn(t, target, session.ID, "for the third host")
	testkit.Eventually(t, "the relay to reach the third host", 15*time.Second, func() bool {
		target.daemon.syncRelays(context.Background(), target.claude, moved)
		return relayed(t, target, listener.ID, reply) == 1
	})
}

// withoutCarries answers a move's prepare as an Agenthail that carries
// nothing with the session.
func withoutCarries(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/session-move/prepare" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		next.ServeHTTP(recorder, r)
		var answer map[string]any
		if json.Unmarshal(recorder.Body.Bytes(), &answer) == nil {
			delete(answer, "carries")
		}
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(recorder.Code)
		_ = json.NewEncoder(w).Encode(answer)
	})
}

func TestATargetThatCannotTakeRelaysLeavesTheSessionAndItsRelayWhereTheyWere(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	target := newMoveNode(t)
	target.handler = withoutCarries(target.handler)
	source, target, record, _ := pairMoveNodes(t, newMoveNode(t), target)
	session := startMovable(t, source)
	listener := startMovable(t, target)
	mirroredOn(t, source, target, record.ID, listener.ID)
	relay := relayFrom(t, source, session, listener.ID)

	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err == nil || !strings.Contains(err.Error(), "update Agenthail on server; it cannot take over the relays") {
		t.Fatalf("moving to a target that cannot take relays returned %v", err)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != "" {
		t.Fatalf("the session left for %q", owner)
	}
	if kept := routesFrom(t, source, session.ID); len(kept) != 1 || kept[0].ID != relay || !kept[0].Active {
		t.Fatalf("the relay is %+v after the refused move", kept)
	}
	if path := claudeTranscriptIn(t, source.host.Home, session.ID); path == "" {
		t.Fatal("the refused move took the conversation's transcript away")
	}
}
