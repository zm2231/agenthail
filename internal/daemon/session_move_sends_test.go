package daemon

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func holdingPrepare(next http.Handler, reached chan<- struct{}, release <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/session-move/prepare" {
			reached <- struct{}{}
			<-release
		}
		next.ServeHTTP(w, r)
	})
}

func TestASendThatArrivesWhileItsSessionIsHeldForAMoveReachesItOnceOnTheNewHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target := newMoveNode(t), newMoveNode(t)
	reached, release := make(chan struct{}, 1), make(chan struct{})
	target.handler = holdingPrepare(target.handler, reached, release)
	pairMoveNodes(t, source, target)
	session := startMovable(t, source)
	claudetest.ListenPeerManager(t, source.host.Home)
	onTarget := claudetest.ListenPeerManager(t, target.host.Home)

	moved := make(chan error, 1)
	go func() {
		_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
		moved <- err
	}()
	<-reached
	sent := make(chan actionReply, 1)
	go func() {
		sent <- sendAsPerson(source.daemon, fmt.Sprintf(`{"action":"send","sessionId":%q,"message":"while it moves"}`, session.ID))
	}()
	var reply actionReply
	select {
	case reply = <-sent:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("the send waited for the move to end")
	}
	close(release)
	if err := <-moved; err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Result struct {
			Evidence string `json:"evidence"`
		} `json:"result"`
	}
	if reply.Status != http.StatusOK || json.Unmarshal(reply.Body, &answer) != nil || answer.Result.Evidence != "queued" {
		t.Fatalf("a send while the session was held for its move answered %d: %s, want it queued", reply.Status, reply.Body)
	}
	host := mustPairedHost(t, source, mustHostID(t, target.federationNode))
	testkit.Eventually(t, "the send to reach the session on its new host", 15*time.Second, func() bool {
		source.daemon.drainHostDeliveries(context.Background(), host)
		return onTarget.Containing("while it moves") >= 1
	})
	source.daemon.drainHostDeliveries(context.Background(), host)
	if got := onTarget.Containing("while it moves"); got != 1 {
		t.Fatalf("the send reached the moved session %d times", got)
	}
}

type staleCatalog struct {
	frozen atomic.Bool
}

func (s *staleCatalog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/catalog-events" {
			w = frozenWriter{ResponseWriter: w, frozen: &s.frozen}
		}
		next.ServeHTTP(w, r)
	})
}

type frozenWriter struct {
	http.ResponseWriter
	frozen *atomic.Bool
}

func (w frozenWriter) Write(data []byte) (int, error) {
	for w.frozen.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	return w.ResponseWriter.Write(data)
}

func (w frozenWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok && !w.frozen.Load() {
		flusher.Flush()
	}
}

func TestASendFromAThirdHostThatHasNotSeenTheMoveReachesTheSessionOnItsNewHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target, third := newMoveNode(t), newMoveNode(t), newMoveNode(t)
	stale := &staleCatalog{}
	source.handler = stale.wrap(source.handler)
	pairMoveNodes(t, source, target)
	source.serve(t, nil)
	roots := x509.NewCertPool()
	roots.AddCert(source.server.Certificate())
	roots.AddCert(target.server.Certificate())
	third.start(t, roots)
	toSource := third.pair(t, source.federationNode, "source")
	third.pair(t, target.federationNode, "server")
	session := startMovable(t, source)
	claudetest.ListenPeerManager(t, source.host.Home)
	onTarget := claudetest.ListenPeerManager(t, target.host.Home)
	mirroredOn(t, third, source, toSource.ID, session.ID)

	stale.frozen.Store(true)
	if _, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"}); err != nil {
		t.Fatal(err)
	}
	if owner, _ := third.reg.SessionHost(session.ID); owner != toSource.ID {
		t.Fatalf("the third host already sees the session on %q", owner)
	}
	reply := sendAsPerson(third.daemon, fmt.Sprintf(`{"action":"send","sessionId":%q,"message":"to where it was"}`, hosts.Qualify(toSource.ID, session.ID)))
	stale.frozen.Store(false)
	if reply.Status != http.StatusOK {
		t.Fatalf("a send to the session's former host answered %d: %s", reply.Status, reply.Body)
	}
	testkit.Eventually(t, "the send to reach the session on its new host", 15*time.Second, func() bool {
		return onTarget.Containing("to where it was") >= 1
	})
	if got := onTarget.Containing("to where it was"); got != 1 {
		t.Fatalf("the send reached the moved session %d times", got)
	}
}
