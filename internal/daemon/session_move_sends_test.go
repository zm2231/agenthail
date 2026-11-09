package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

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
