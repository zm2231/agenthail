package daemon

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func ownRevision(t *testing.T, d *Daemon) string {
	t.Helper()
	rev, err := d.ownDecisionsRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func decisionsEvents(t *testing.T, d *Daemon) []map[string]string {
	t.Helper()
	window, err := d.Registry.CatalogEventsAfter(0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var found []map[string]string
	for _, event := range window.Events {
		if event.Type != "decisions.changed" {
			continue
		}
		var payload map[string]string
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		found = append(found, payload)
	}
	return found
}

func TestTheDecisionsRevisionMovesOnlyWhenADecisionCan(t *testing.T) {
	f := newMCPAnswerFixture(t)
	quiet := ownRevision(t, f.d)
	if again := ownRevision(t, f.d); again != quiet {
		t.Fatalf("nothing changed and the revision moved: %s then %s", quiet, again)
	}

	if _, err := f.r.QueueMessageWithOptions("worker", "a note from the person", "person-note", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	f.d.discoverCatalog(t.Context())
	if moved := ownRevision(t, f.d); moved != quiet {
		t.Fatalf("a person's queued note holds no one up, yet the revision moved")
	}

	f.waitOnPrompt(t, "worker")
	handled := ownRevision(t, f.d)
	if handled == quiet {
		t.Fatal("a worker's prompt its lead handles left the revision alone")
	}

	f.waitOnPrompt(t, "worker", "lead")
	waiting := ownRevision(t, f.d)
	if waiting == handled {
		t.Fatal("the lead waiting on the person too left the revision alone")
	}

	if _, err := f.r.QueueMessageWithOptions("lead", "the build is ready for you", "worker-report", surface.SendOptions{SourceSessionID: "worker"}); err != nil {
		t.Fatal(err)
	}
	if holding := ownRevision(t, f.d); holding == waiting {
		t.Fatal("a worker held up behind the lead left the revision alone")
	}
}

func (f *mcpAnswerFixture) openRequests(t *testing.T, id string, since time.Time, open ...string) {
	t.Helper()
	waiting := &surface.Waiting{Kind: surface.WaitingApproval, ID: open[0], Answerable: true, Since: since, Open: open}
	session := f.surface.sessions[id]
	session.Status, session.WaitingOn = surface.StatusBusy, waiting
	f.surface.sessions[id] = session
	f.surface.observations[id] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t-" + id, WaitingOn: waiting}
	testkit.Eventually(t, "the open requests in the session view", 5*time.Second, func() bool {
		f.d.scanAndRelay(t.Context())
		f.d.discoverCatalog(t.Context())
		rows, _, err := f.d.decisionRows(t.Context())
		if err != nil {
			return false
		}
		for _, row := range rows {
			if row.ID == id {
				return row.WaitingOn != nil && slices.Equal(askRefs(f.r, row.WaitingOn.Open), open)
			}
		}
		return false
	})
}

func TestASecondOpenRequestMovesTheDecisionsRevision(t *testing.T) {
	f := newMCPAnswerFixture(t)
	since := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	f.openRequests(t, "lead", since, "a")
	one := ownRevision(t, f.d)
	f.openRequests(t, "lead", since, "a", "b")
	two := ownRevision(t, f.d)
	if two == one {
		t.Fatal("a second open request left the decisions revision alone")
	}
	f.openRequests(t, "lead", since, "a")
	if back := ownRevision(t, f.d); back == two {
		t.Fatal("the second request closing left the decisions revision alone")
	}
}

func TestTheRevisionIsPublishedOncePerChangeAndAPeerMovesOnlyTheCombinedRevision(t *testing.T) {
	f := newMCPAnswerFixture(t)
	f.d.refreshDecisionsRevision(t.Context())
	f.d.refreshDecisionsRevision(t.Context())
	events := decisionsEvents(t, f.d)
	own, rev := f.d.decisions.current()
	if len(events) != 1 || own == "" || rev != own || events[0]["own"] != own || events[0]["rev"] != rev {
		t.Fatalf("one change, one event naming it: own=%q rev=%q events=%v", own, rev, events)
	}

	f.d.decisions.setPeer("host-b", "peer-1")
	f.d.refreshDecisionsRevision(t.Context())
	withPeer := decisionsEvents(t, f.d)
	ownNow, combined := f.d.decisions.current()
	if len(withPeer) != 2 || ownNow != own || combined == own || withPeer[1]["own"] != own {
		t.Fatalf("a peer's change moves only the combined revision: own=%q combined=%q events=%v", ownNow, combined, withPeer)
	}

	f.d.decisions.setPeer("host-b", "")
	f.d.refreshDecisionsRevision(t.Context())
	if _, back := f.d.decisions.current(); back != own || len(decisionsEvents(t, f.d)) != 3 {
		t.Fatalf("an unpaired peer leaves the host's own revision: %q", back)
	}
}

func TestAPairedHostsOwnRevisionReachesTheMirrorAndTheSnapshot(t *testing.T) {
	local, remote, record := pairedFederation(t)
	remote.daemon.refreshDecisionsRevision(t.Context())
	remoteOwn, _ := remote.daemon.decisions.current()
	if remoteOwn == "" {
		t.Fatal("the remote worked out no revision")
	}
	testkit.Eventually(t, "the remote's revision mirrored", 10*time.Second, func() bool {
		local.daemon.decisions.mu.Lock()
		defer local.daemon.decisions.mu.Unlock()
		return local.daemon.decisions.peers[record.ID] == remoteOwn
	})
	local.daemon.refreshDecisionsRevision(t.Context())
	own, rev := local.daemon.decisions.current()
	if own == "" || rev == own {
		t.Fatalf("the local combined revision covers the remote: own=%q rev=%q", own, rev)
	}
	if snapshot := local.snapshot(t); snapshot.DecisionsRev != rev || snapshot.OwnDecisionsRev != own {
		t.Fatalf("the snapshot names the revisions: %q %q", snapshot.DecisionsRev, snapshot.OwnDecisionsRev)
	}
}
