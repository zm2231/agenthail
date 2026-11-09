package daemon

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

// moveMesh is three hosts each paired with the other two, the first's
// checkout movable to the second. wrap, when given, wraps the first's
// handler before it serves.
func moveMesh(t *testing.T, wrap func(http.Handler) http.Handler) (a, b, c *moveNode) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	a, b, c = newMoveNode(t), newMoveNode(t), newMoveNode(t)
	if wrap != nil {
		a.handler = wrap(a.handler)
	}
	checkoutsForMove(t, a, b)
	nodes := map[string]*moveNode{"a": a, "b": b, "c": c}
	for _, node := range nodes {
		node.serve(t, nil)
	}
	for _, node := range nodes {
		roots := x509.NewCertPool()
		for _, other := range nodes {
			if other != node {
				roots.AddCert(other.server.Certificate())
			}
		}
		node.start(t, roots)
	}
	paired := map[*moveNode][]string{}
	for _, node := range nodes {
		for name, other := range nodes {
			if other != node {
				paired[node] = append(paired[node], node.pair(t, other.federationNode, name).ID)
			}
		}
	}
	testkit.Eventually(t, "the hosts connected", 15*time.Second, func() bool {
		for node, ids := range paired {
			for _, id := range ids {
				if !node.daemon.hostConnected(id) {
					return false
				}
			}
		}
		return true
	})
	return a, b, c
}

type leadSample struct {
	at       time.Time
	host     string
	lead     string
	err      error
	rowsLeft int
}

// sampleLead reads a host's lead between two reads of its coordinator rows.
// rowsLeft counts the rows listed by both reads, so a lookup that failed
// while they were listed failed with a row in place throughout.
func sampleLead(node *moveNode, name, projectID string) leadSample {
	sample := leadSample{at: time.Now(), host: name}
	before := leadRows(node, projectID)
	spec, err := node.daemon.coordinator().ProjectLead(projectID, "widget")
	sample.err = err
	if err == nil {
		sample.lead = spec.SessionID
		if _, raw, ok := hosts.Split(spec.SessionID); ok {
			sample.lead = raw
		}
	}
	for host := range leadRows(node, projectID) {
		if before[host] {
			sample.rowsLeft++
		}
	}
	return sample
}

func leadRows(node *moveNode, projectID string) map[string]bool {
	rows, _ := node.reg.Coordinators()
	listed := map[string]bool{}
	for _, row := range rows {
		if row.ProjectID == projectID {
			listed[row.HostID] = true
		}
	}
	return listed
}

func TestALeadMovesWithItsSessionAndEveryHostSeesOneLeadThroughout(t *testing.T) {
	a, b, c := moveMesh(t, nil)
	project, session, since := leadOn(t, a, b, c)
	onA, onB := claudetest.ListenPeerManager(t, a.host.Home), claudetest.ListenPeerManager(t, b.host.Home)
	nodes := map[string]*moveNode{"a": a, "b": b, "c": c}

	var samples []leadSample
	var replies []actionReply
	var mu sync.Mutex
	stop := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for name, node := range nodes {
				sample := sampleLead(node, name, project.ID)
				mu.Lock()
				samples = append(samples, sample)
				mu.Unlock()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	go func() {
		defer group.Done()
		for number := 1; ; number++ {
			select {
			case <-stop:
				return
			default:
			}
			reply := sendAsPerson(c.daemon, fmt.Sprintf(`{"action":"coordinator-send","project":%q,"message":"lead message %d."}`, project.ID, number))
			mu.Lock()
			replies = append(replies, reply)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
	}()
	time.Sleep(200 * time.Millisecond)
	if _, err := a.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "b"}); err != nil {
		close(stop)
		group.Wait()
		t.Fatal(err)
	}
	testkit.Eventually(t, "every host to see the lead on its new host", 15*time.Second, func() bool {
		spec, err := c.daemon.coordinator().ProjectLead(project.ID, "widget")
		return err == nil && spec.HostID == mustHostID(t, b.federationNode)
	})
	time.Sleep(300 * time.Millisecond)
	close(stop)
	group.Wait()

	for index, reply := range replies {
		if reply.Status != http.StatusOK {
			t.Errorf("send %d answered %d: %s", index+1, reply.Status, reply.Body)
		}
	}
	for number := 1; number <= len(replies); number++ {
		text := fmt.Sprintf("lead message %d.", number)
		testkit.Eventually(t, text+" delivered", 20*time.Second, func() bool {
			return onA.Containing(text)+onB.Containing(text) >= 1
		})
	}
	for number := 1; number <= len(replies); number++ {
		text := fmt.Sprintf("lead message %d.", number)
		if got := onA.Containing(text) + onB.Containing(text); got != 1 {
			t.Errorf("%s delivered %d times", text, got)
		}
	}

	none := map[string]time.Time{}
	longest := map[string]time.Duration{}
	for _, sample := range samples {
		switch {
		case sample.err == nil && sample.lead != session.ID:
			t.Fatalf("%s saw lead %q at %s, want %s", sample.host, sample.lead, sample.at, session.ID)
		case sample.err != nil && strings.Contains(sample.err.Error(), "has coordinators"):
			t.Fatalf("%s saw two leads at %s: %v", sample.host, sample.at, sample.err)
		case sample.err != nil && sample.rowsLeft > 0:
			t.Fatalf("%s saw no lead at %s while it listed %d coordinator rows: %v", sample.host, sample.at, sample.rowsLeft, sample.err)
		case sample.err != nil:
			if _, open := none[sample.host]; !open {
				none[sample.host] = sample.at
			}
		default:
			if began, open := none[sample.host]; open {
				longest[sample.host] = max(longest[sample.host], sample.at.Sub(began))
				delete(none, sample.host)
			}
		}
	}
	t.Logf("%d sends, %d samples; longest time without a lead per host: %v", len(replies), len(samples), longest)

	for name, node := range map[string]*moveNode{"a": a, "b": b} {
		history, err := node.reg.CoordinatorHistory(project.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range history {
			if entry.Action == "replaced" {
				t.Fatalf("%s replaced the lead's conversation during the move: %+v", name, entry)
			}
		}
	}
	listing, err := claudehost.NewClient(b.host.Home).List(context.Background())
	if err != nil || len(listing.Sessions) != 1 || listing.Sessions[0].SessionID != session.ID {
		t.Fatalf("the new host runs %+v err=%v, want only the moved lead", listing.Sessions, err)
	}
	spec, err := b.reg.Coordinator(project.ID)
	if err != nil || spec.SessionID != session.ID || spec.Cwd == a.host.Cwd || spec.Instructions != "keep the widget shipping" {
		t.Fatalf("the new host's coordinator is %+v err=%v", spec, err)
	}
	if moved, held, err := b.reg.CoordinatorSince(project.ID, session.ID); err != nil || !held || !moved.Equal(since) {
		t.Fatalf("the lead is lead since %s held=%v err=%v, want since %s", moved, held, err, since)
	}
	if _, err := a.reg.Coordinator(project.ID); !errors.Is(err, registry.ErrNoCoordinator) {
		t.Fatalf("the former host keeps its coordinator: %v", err)
	}
}

// leadOn makes a movable session on a the lead of its repository's project,
// which b knows by the same repository, and waits until every host sees it.
func leadOn(t *testing.T, a, b, c *moveNode) (*CatalogProject, *surface.Session, time.Time) {
	t.Helper()
	project, err := ResolvePathProject(context.Background(), a.reg, a.host.Cwd)
	if err != nil || project == nil || !strings.HasPrefix(project.ID, hosts.RepositoryPrefix) {
		t.Fatalf("the source's project is %+v err=%v, want a repository's", project, err)
	}
	if err := b.reg.SetProjectRepository("project-widget", project.ID, nil); err != nil {
		t.Fatal(err)
	}
	session := startMovable(t, a)
	if _, err := a.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.ID, ProjectRoot: project.RootPath, Name: "widget-lead", Surface: "claude", Cwd: a.host.Cwd, Instructions: "keep the widget shipping"}, registry.CoordinatorChange{SessionID: session.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	since, _, err := a.reg.CoordinatorSince(project.ID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.daemon.flushCatalog("coordinator " + project.ID)
	testkit.Eventually(t, "every host to see the lead", 15*time.Second, func() bool {
		for name, node := range map[string]*moveNode{"a": a, "b": b, "c": c} {
			if sample := sampleLead(node, name, project.ID); sample.lead != session.ID {
				return false
			}
		}
		return true
	})
	return project, session, since
}

func TestASendToAProjectFromAHostThatHasNotSeenItsLeadMoveReachesTheLeadOnItsNewHost(t *testing.T) {
	stale := &staleCatalog{}
	a, b, c := moveMesh(t, stale.wrap)
	project, session, _ := leadOn(t, a, b, c)
	claudetest.ListenPeerManager(t, a.host.Home)
	onB := claudetest.ListenPeerManager(t, b.host.Home)

	stale.frozen.Store(true)
	if _, err := a.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "b"}); err != nil {
		t.Fatal(err)
	}
	if spec, err := c.daemon.coordinator().ProjectLead(project.ID, "widget"); err != nil || spec.HostID != mustHostID(t, a.federationNode) {
		t.Fatalf("the third host already sees the lead as %+v err=%v", spec, err)
	}
	reply := sendAsPerson(c.daemon, fmt.Sprintf(`{"action":"coordinator-send","project":%q,"message":"to the lead where it was."}`, project.ID))
	stale.frozen.Store(false)
	if reply.Status != http.StatusOK {
		t.Fatalf("a send to the project whose lead moved answered %d: %s", reply.Status, reply.Body)
	}
	testkit.Eventually(t, "the send to reach the lead on its new host", 15*time.Second, func() bool {
		return onB.Containing("to the lead where it was.") >= 1
	})
	if got := onB.Containing("to the lead where it was."); got != 1 {
		t.Fatalf("the send reached the moved lead %d times", got)
	}
}

func TestATargetThatCannotTakeTheLeadLeavesTheLeadWhereItWas(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	source, target := newMoveNode(t), newMoveNode(t)
	target.handler = withoutCarries(target.handler)
	moveBothWays(t, source, target)
	project, err := ResolvePathProject(context.Background(), source.reg, source.host.Cwd)
	if err != nil || project == nil {
		t.Fatalf("the source's project is %+v err=%v", project, err)
	}
	if err := target.reg.SetProjectRepository("project-widget", project.ID, nil); err != nil {
		t.Fatal(err)
	}
	session := startMovable(t, source)
	if _, err := source.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.ID, ProjectRoot: project.RootPath, Name: "widget-lead", Surface: "claude", Cwd: source.host.Cwd}, registry.CoordinatorChange{SessionID: session.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}

	_, err = source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err == nil || !strings.Contains(err.Error(), "update Agenthail on server; it cannot take over the coordinator role") {
		t.Fatalf("moving to a target that cannot take the lead returned %v", err)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != "" {
		t.Fatalf("the session left for %q", owner)
	}
	if spec, err := source.reg.Coordinator(project.ID); err != nil || spec.SessionID != session.ID {
		t.Fatalf("the lead after the refused move is %+v err=%v", spec, err)
	}
	if _, err := target.reg.Coordinator(project.ID); !errors.Is(err, registry.ErrNoCoordinator) {
		t.Fatalf("the target took a lead it cannot hold: %v", err)
	}
}
