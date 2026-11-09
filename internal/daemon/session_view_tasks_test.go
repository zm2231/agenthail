package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestATaskFoldsUnderItsStarterApartFromItsWorkers(t *testing.T) {
	f := newWorkerFixture()
	task := func(r *dashboardSession) { r.Task = true }
	f.add("lead", "", "main")
	f.add("worker", "lead", "main")
	f.add("running", "lead", "main", task, func(r *dashboardSession) { r.Status = surface.StatusBusy })
	f.add("asking", "lead", "main", task, func(r *dashboardSession) { r.Attention = &SessionAttention{Kind: AttentionAsk} })
	f.add("done", "lead", "main", task)
	f.add("task-of-task", "running", "main", task)
	plan := f.plan(SessionViewFilters{})

	lead, _ := familyOf(t, plan, "lead")
	for id, depth := range map[string]int{"running": 1, "asking": 1, "done": 1, "task-of-task": 2} {
		family, member := familyOf(t, plan, id)
		if family.RootID != "lead" || member.Depth != depth || !member.Task || member.Worker {
			t.Fatalf("%s is %+v in %s, want a task at depth %d under lead", id, member, family.RootID, depth)
		}
	}
	if _, member := familyOf(t, plan, "worker"); !member.Worker || member.Task {
		t.Fatalf("the worker is %+v", member)
	}
	if lead.Workers == nil || *lead.Workers != (SessionViewWorkers{Count: 1}) {
		t.Fatalf("workers=%+v, want only the worker", lead.Workers)
	}
	if lead.Tasks == nil || *lead.Tasks != (SessionViewWorkers{Count: 4, Working: 1, NeedsYou: 1}) {
		t.Fatalf("tasks=%+v, want 4 with 1 working and 1 needing you", lead.Tasks)
	}
	if lead.key.Band != sessionBandNeedsYou {
		t.Fatalf("a task that needs you left its family in band %v", lead.key.Band)
	}
}

func TestATaskWithoutAPlaceUnderItsStarterIsAPlainSession(t *testing.T) {
	f := newWorkerFixture()
	task := func(r *dashboardSession) { r.Task = true }
	f.add("lead", "", "main")
	f.add("elsewhere", "lead", "topic", task)
	f.add("of-missing", "ghost", "main", task)
	f.add("archived", "lead", "main", task, func(r *dashboardSession) { r.Archived = true })
	plan := f.plan(SessionViewFilters{})
	for _, id := range []string{"elsewhere", "of-missing"} {
		if family, member := familyOf(t, plan, id); family.RootID != id || member.Task || family.Tasks != nil {
			t.Fatalf("%s is %+v in %+v, want its own root without a task group", id, member, family)
		}
	}
	if lead, _ := familyOf(t, plan, "lead"); lead.Tasks != nil || len(lead.Members) != 1 {
		t.Fatalf("the starter's family is %+v, want no task group", lead)
	}
	archived := newSessionViewPlan(f.rows, "archived", SessionViewFilters{}, f.now)
	if family, member := familyOf(t, archived, "archived"); family.RootID != "archived" || member.Task || family.Tasks != nil {
		t.Fatalf("in Archived, an archived task of a live starter is %+v in %+v, want its own root", member, family)
	}
}

func TestATaskRecordedAfterTheRowIsPublishedReachesEveryRead(t *testing.T) {
	d, r, fake, parent, child := daemonFixture(t)
	for _, session := range []surface.Session{parent, child} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
	}
	d.discoverCatalog(context.Background())
	if viewSession(t, d, child.ID).Task {
		t.Fatal("setup: a task before any task was recorded")
	}
	if _, err := r.CreateTask(parent.ID, child.ID, "look", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !viewSession(t, d, child.ID).Task {
		t.Fatal("the cached view missed the task")
	}
	if viewSession(t, d, parent.ID).Task {
		t.Fatal("the starter became a task")
	}
	d.discoverCatalog(context.Background())
	fingerprint, _, err := r.CatalogProjectionFingerprint(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	var published dashboardSession
	if err := json.Unmarshal([]byte(fingerprint), &published); err != nil || !published.Task {
		t.Fatalf("the published row has task=%t (%v), want true", published.Task, err)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range state.Sessions {
		if (row.ID == child.ID) != row.Task {
			t.Fatalf("the unscoped snapshot has %s task=%t", row.ID, row.Task)
		}
	}
}

// A task's row lives on its child's host, so a mirrored row carries that host's flag.
func TestAMirroredTaskKeepsTheFlagItsOwnHostPublished(t *testing.T) {
	_, store := openPresentationRegistry(t)
	recordPresentation(t, store, surface.Session{ID: "ours", Name: "ours", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}, dashboardSession{})
	record, err := store.AddHost(registry.PairedHost{ID: "h0123456789ab", Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, store, nil)
	peer := &pairedHost{record: record}
	for id, task := range map[string]bool{"peer-lead": false, "peer-task": true} {
		row := dashboardSession{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Task: task}
		if task {
			row.StartedBy = "peer-lead"
		}
		if err := d.mirrorRemoteRow(peer, row, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateTask("ours", "peer-lead", "a local row naming the peer's raw id", time.Now()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{record.ID + "::peer-task": true, record.ID + "::peer-lead": false, "ours": false} {
		if got := viewSession(t, d, id).Task; got != want {
			t.Errorf("%s task=%t, want %t", id, got, want)
		}
	}
}
