package daemon

import (
	"context"
	"crypto/x509"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

// moveBothWays is a source and a target paired with each other, the
// source's checkout movable to the target.
func moveBothWays(t *testing.T, source, target *moveNode) (toTarget, toSource registry.PairedHost) {
	t.Helper()
	checkoutsForMove(t, source, target)
	source.serve(t, nil)
	target.serve(t, nil)
	target.start(t, testRoots(source.server))
	toSource = target.pair(t, source.federationNode, "home")
	roots := x509.NewCertPool()
	roots.AddCert(target.server.Certificate())
	source.start(t, roots)
	toTarget = source.pair(t, target.federationNode, "server")
	testkit.Eventually(t, "the hosts connected", 10*time.Second, func() bool {
		return source.daemon.hostConnected(toTarget.ID) && target.daemon.hostConnected(toSource.ID)
	})
	return toTarget, toSource
}

func channelMovePair(t *testing.T) (source, target *moveNode, toTarget, toSource registry.PairedHost) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target = newMoveNode(t), newMoveNode(t)
	toTarget, toSource = moveBothWays(t, source, target)
	return source, target, toTarget, toSource
}

func inChannel(t *testing.T, node *moveNode, channel string, sessions ...string) {
	t.Helper()
	if _, err := node.reg.CreateChannel(channel); err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if err := node.reg.AddToChannel(channel, session); err != nil {
			t.Fatal(err)
		}
	}
}

func movedHere(t *testing.T, source *moveNode, session *surface.Session) {
	t.Helper()
	if _, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"}); err != nil {
		t.Fatal(err)
	}
}

func TestAMovedSessionPostsToItsChannelThroughTheHostThatKeepsIt(t *testing.T) {
	source, target, _, toSource := channelMovePair(t)
	session := startMovable(t, source)
	member := startMovable(t, source)
	inChannel(t, source, "builders", session.ID, member.ID)
	onSource := claudetest.ListenPeerManager(t, source.host.Home)
	onTarget := claudetest.ListenPeerManager(t, target.host.Home)

	movedHere(t, source, session)
	if home, found, err := target.reg.RemoteChannel(session.ID, "builders"); err != nil || !found || home != toSource.ID {
		t.Fatalf("the target keeps #builders for the session at %q found=%v err=%v", home, found, err)
	}
	code, answer := channelSend(t, target.federationNode, "", map[string]any{"action": "channel-send", "channel": "#builders", "message": "from the new host", "sourceSessionId": session.ID})
	if code != http.StatusOK || len(answer.Hosts) != 1 || answer.Hosts[0].State != channelLegReached {
		t.Fatalf("status=%d answer=%+v, want the post reached the channel's host", code, answer)
	}
	if len(answer.Recipients) != 1 || answer.Recipients[0].SessionID != hosts.Qualify(toSource.ID, member.ID) {
		t.Fatalf("recipients=%+v, want only the member on the channel's host, not the sender", answer.Recipients)
	}
	testkit.Eventually(t, "the member to get the post", 15*time.Second, func() bool { return onSource.Containing("from the new host") == 1 })

	reply := source.daemon.doAction(context.Background(), principal{ID: localDashboardPrincipal, Kind: principalPerson}, dashboardActionRequest{Action: "channel-send", Channel: "#builders", Message: "for the moved session", SourceSessionID: member.ID})
	if reply.Status != http.StatusOK {
		t.Fatalf("a post on the channel's host answered %d: %s", reply.Status, reply.Body)
	}
	testkit.Eventually(t, "the moved session to get the post on its new host", 15*time.Second, func() bool {
		return onTarget.Containing("for the moved session") == 1
	})
	if got := onSource.Containing("for the moved session"); got != 0 {
		t.Fatalf("the channel's host delivered its own post %d times on this host", got)
	}
}

func TestAPostFromAMovedSessionWhileItsChannelsHostIsUnreachableIsDeliveredOnceItReconnects(t *testing.T) {
	source, target, _, toSource := channelMovePair(t)
	session := startMovable(t, source)
	member := startMovable(t, source)
	inChannel(t, source, "builders", session.ID, member.ID)
	onSource := claudetest.ListenPeerManager(t, source.host.Home)
	onTarget := claudetest.ListenPeerManager(t, target.host.Home)
	movedHere(t, source, session)

	source.gate.proxyDown.Store(true)
	code, answer := channelSend(t, target.federationNode, "", map[string]any{"action": "channel-send", "channel": "#builders", "message": "while you were away", "sourceSessionId": session.ID})
	if code != http.StatusOK || len(answer.Hosts) != 1 || answer.Hosts[0].State != channelLegHeld && answer.Hosts[0].State != channelLegUnknown || len(answer.Unreachable) != 1 {
		t.Fatalf("status=%d answer=%+v, want the post held for the channel's host", code, answer)
	}
	if held, err := target.reg.HostHeldActions(toSource.ID); err != nil || len(held) != 1 {
		t.Fatalf("held=%+v err=%v, want the post held once", held, err)
	}
	source.gate.proxyDown.Store(false)
	host, _ := target.daemon.pairedHost(toSource.ID)
	testkit.Eventually(t, "the held post replayed", 15*time.Second, func() bool {
		target.daemon.drainHostDeliveries(context.Background(), host)
		held, err := target.reg.HostHeldActions(toSource.ID)
		return err == nil && len(held) == 0
	})
	testkit.Eventually(t, "the member to get the post", 15*time.Second, func() bool { return onSource.Containing("while you were away") >= 1 })
	target.daemon.drainHostDeliveries(context.Background(), host)
	if got := onSource.Containing("while you were away"); got != 1 {
		t.Fatalf("the channel's host delivered the held post %d times", got)
	}
	if got := onTarget.Containing("while you were away"); got != 0 {
		t.Fatalf("the sender got its own post %d times", got)
	}
}

func TestAMovedSessionThatLeftItsChannelStopsPostingToIt(t *testing.T) {
	source, target, _, _ := channelMovePair(t)
	session := startMovable(t, source)
	member := startMovable(t, source)
	inChannel(t, source, "builders", session.ID, member.ID)
	onSource := claudetest.ListenPeerManager(t, source.host.Home)
	movedHere(t, source, session)
	if err := source.reg.RemoveFromChannel("builders", session.ID); err != nil {
		t.Fatal(err)
	}

	code, answer := channelSend(t, target.federationNode, "", map[string]any{"action": "channel-send", "channel": "#builders", "message": "after leaving", "sourceSessionId": session.ID})
	if code == http.StatusOK || len(answer.Hosts) != 1 || answer.Hosts[0].State != channelLegRefused || answer.Hosts[0].Rule != channelRuleNotMember {
		t.Fatalf("status=%d answer=%+v, want the channel's host to refuse a session that left", code, answer)
	}
	if _, found, err := target.reg.RemoteChannel(session.ID, "builders"); err != nil || found {
		t.Fatalf("the target still keeps #builders for the session: found=%v err=%v", found, err)
	}
	if got := onSource.Containing("after leaving"); got != 0 {
		t.Fatalf("the member got a post from a session that left %d times", got)
	}
}

func TestATargetThatCannotTakeChannelsLeavesTheSessionInItsChannel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target := newMoveNode(t), newMoveNode(t)
	target.handler = withoutCarries(target.handler)
	moveBothWays(t, source, target)
	session := startMovable(t, source)
	inChannel(t, source, "builders", session.ID)

	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err == nil || !strings.Contains(err.Error(), "update Agenthail on server; it cannot take over the channels") {
		t.Fatalf("moving to a target that cannot take channels returned %v", err)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != "" {
		t.Fatalf("the session left for %q", owner)
	}
	if members, err := source.reg.ChannelMembers("builders"); err != nil || len(members) != 1 || members[0] != session.ID {
		t.Fatalf("members=%v err=%v after the refused move", members, err)
	}
}
