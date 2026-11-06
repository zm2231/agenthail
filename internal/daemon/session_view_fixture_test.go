package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type viewWrite struct {
	name string
	do   func(*rand.Rand)
}

type viewFixture struct {
	d        *Daemon
	reg      *registry.Registry
	ctx      context.Context
	clock    time.Time
	requests []sessionViewRequest
	writes   []viewWrite
	fill     func()
}

func (f *viewFixture) read(t *testing.T, request sessionViewRequest, uncached bool) (string, SessionViewSnapshot) {
	t.Helper()
	f.d.presentation.uncached = uncached
	defer func() { f.d.presentation.uncached = false }()
	state, err := f.d.readSessionView(f.ctx, request)
	if err != nil {
		return "error: " + err.Error(), state
	}
	state.PresentationPosition.Sequence = 0
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded), state
}

func (f *viewFixture) populate(rng *rand.Rand, steps int, after func(step int, write string)) {
	for step := range steps {
		write := f.writes[rng.Intn(len(f.writes))]
		write.do(rng)
		after(step, write.name)
	}
}

func newViewFixture(t *testing.T) *viewFixture {
	d, reg, fake, _, _, cwd := baseFixture(t)
	f := &viewFixture{d: d, reg: reg, ctx: context.Background(), clock: time.Now().UTC()}
	d.presentation.clock = func() time.Time { return f.clock }
	handler := d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	hostID := "h0123456789ab"
	if _, err := reg.AddHost(registry.PairedHost{ID: hostID, Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	mirror := func(id string, status surface.SessionStatus) {
		session := surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: status}
		raw, _ := json.Marshal(dashboardSession{ID: id, Name: id, Surface: session.Surface, Status: status, Host: &dashboardHost{ID: hostID, Name: "peer", State: "connected"}})
		if _, _, err := reg.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: session, EntityID: hostID + "::" + id, Projection: raw, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: f.clock}); err != nil {
			t.Fatal(err)
		}
	}
	mirror("far", surface.StatusIdle)
	here := registry.ProjectRecord{ID: "project-here", WorkspaceRoot: cwd, Title: "here", Added: true, CreatedAt: f.clock}
	if err := reg.AddProject(here); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		session := surface.Session{ID: fmt.Sprint("s", i), Name: fmt.Sprint("s", i), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: cwd, LastActive: f.clock.Add(-time.Duration(i*7) * time.Hour)}
		if i == 5 {
			session.Subagent = &surface.Subagent{ParentID: "s4", RootID: "s4"}
		}
		fake.sessions[session.ID] = session
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(f.ctx)

	f.requests = []sessionViewRequest{
		{Scope: "current", Limit: 200, Members: true},
		{Scope: "all", Limit: 200, Members: true},
		{Scope: "all", Limit: 2},
		{Scope: "archived", Limit: 200, Members: true},
		{Scope: "running", Limit: 200},
		{Scope: "recent", Limit: 200, Members: true},
		{Scope: "all", Limit: 200, Filters: SessionViewFilters{Collapsed: true}},
		{Scope: "all", Limit: 200, Filters: SessionViewFilters{Query: "s1"}},
		{Scope: "current", Limit: 200, Filters: SessionViewFilters{Attention: true}},
		{Scope: "all", Limit: 200, Filters: SessionViewFilters{Status: "idle", Surface: "codex"}},
	}

	session := func(rng *rand.Rand) string { return fmt.Sprint("s", rng.Intn(5)) }
	var intents []int64
	f.writes = []viewWrite{
		{"an alias", func(rng *rand.Rand) {
			_ = reg.SetAlias(fmt.Sprint("name-", rng.Intn(1000)), session(rng))
		}},
		{"a failed delivery", func(rng *rand.Rand) {
			intent, err := reg.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: session(rng), TargetSessionID: session(rng), Status: registry.DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reg.FailDeliveryIntent(intent.ID, "not delivered"); err != nil {
				t.Fatal(err)
			}
			intents = append(intents, intent.ID)
		}},
		{"a sent message", func(rng *rand.Rand) {
			sender, target := session(rng), session(rng)
			if len(intents) > 0 && rng.Intn(2) == 0 {
				failed, err := reg.DeliveryIntent(intents[rng.Intn(len(intents))])
				if err != nil {
					t.Fatal(err)
				}
				sender, target = failed.SenderSessionID, failed.TargetSessionID
			}
			if _, err := reg.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: sender, TargetSessionID: target, Status: registry.DeliveryIntentSent, Evidence: surface.EvidenceTransportAccepted}); err != nil {
				t.Fatal(err)
			}
		}},
		{"a dismissed delivery problem", func(rng *rand.Rand) {
			if len(intents) > 0 {
				if _, err := reg.DismissDeliveryProblem(intents[rng.Intn(len(intents))]); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"a queued message", func(rng *rand.Rand) {
			if err := reg.QueueMessage(session(rng), "queued"); err != nil {
				t.Fatal(err)
			}
		}},
		{"a dead message needing attention", func(rng *rand.Rand) {
			id := session(rng)
			if err := reg.QueueMessage(id, "dying"); err != nil {
				t.Fatal(err)
			}
			if message, err := reg.ClaimNextMessage(id, f.clock); err == nil && message != nil {
				if _, err := reg.DeadLetterMessage(message.ID, errors.New("gone")); err != nil {
					t.Fatal(err)
				}
			}
			if err := reg.ReconcileAttentionItems(time.Now()); err != nil {
				t.Fatal(err)
			}
		}},
		{"an archived or restored session", func(rng *rand.Rand) {
			id := session(rng)
			if rng.Intn(2) == 0 {
				_ = reg.ArchiveSession(id, f.clock)
			} else {
				_ = reg.UnarchiveSession(id)
			}
		}},
		{"a project", func(rng *rand.Rand) {
			project := registry.ProjectRecord{ID: fmt.Sprint("project-", rng.Intn(3)), WorkspaceRoot: filepath.Join(t.TempDir(), "work"), Title: "work", Added: true, CreatedAt: f.clock}
			_ = reg.AddProject(project)
			if rng.Intn(2) == 0 {
				_ = reg.ArchiveProject(project, f.clock)
			} else {
				_ = reg.UnarchiveProject(project.ID)
			}
		}},
		{"a lead", func(rng *rand.Rand) {
			response := postAction(t, handler, map[string]any{"action": "coordinator-set", "sessionId": session(rng), "name": "lead-a", "replace": true})
			if response.Code != http.StatusOK && response.Code != http.StatusConflict && response.Code != http.StatusBadRequest {
				t.Fatalf("lead: %d %s", response.Code, response.Body.String())
			}
		}},
		{"a usage limit stop", func(rng *rand.Rand) {
			id := session(rng)
			if rng.Intn(2) == 0 {
				if err := reg.RecordLimitStop(fake.sessions[id], "account", surface.LimitStop{TurnID: "turn", RefusedAt: f.clock, ResetsAt: f.clock.Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			} else if err := reg.ClearLimitStop(id, "turn"); err != nil {
				t.Fatal(err)
			}
		}},
		{"a catalog change", func(rng *rand.Rand) {
			id := session(rng)
			changed := fake.sessions[id]
			changed.Status = []surface.SessionStatus{surface.StatusIdle, surface.StatusBusy}[rng.Intn(2)]
			changed.LastActive = f.clock.Add(-time.Duration(rng.Intn(48)) * time.Hour)
			fake.sessions[id] = changed
			if err := reg.RegisterSession(changed); err != nil {
				t.Fatal(err)
			}
			d.discoverCatalog(f.ctx)
		}},
		{"attention reconciled on its own", func(*rand.Rand) {
			if err := reg.ReconcileAttentionItems(f.clock); err != nil {
				t.Fatal(err)
			}
		}},
		{"the sessions' project archived or restored", func(rng *rand.Rand) {
			if rng.Intn(2) == 0 {
				_ = reg.ArchiveProject(here, f.clock)
			} else {
				_ = reg.UnarchiveProject(here.ID)
			}
		}},
		{"a lead set outside the daemon", func(rng *rand.Rand) {
			spec := registry.CoordinatorSpec{ProjectID: here.ID, ProjectRoot: cwd, Cwd: cwd, Name: "lead-b", Surface: string(surface.KindCodex), SessionID: session(rng), ChangedAt: f.clock}
			var taken registry.AliasTakenError
			var named registry.CoordinatorNameTakenError
			if _, err := reg.SetCoordinator(spec, registry.CoordinatorChange{SessionID: spec.SessionID, Replace: true}); err != nil && !errors.As(err, &taken) && !errors.As(err, &named) {
				t.Fatal(err)
			}
		}},
		{"a session row with no catalog event", func(rng *rand.Rand) {
			changed := fake.sessions[session(rng)]
			changed.Status = []surface.SessionStatus{surface.StatusIdle, surface.StatusBusy}[rng.Intn(2)]
			changed.LastActive = f.clock.Add(-time.Duration(rng.Intn(48)) * time.Hour)
			if err := reg.RegisterSession(changed); err != nil {
				t.Fatal(err)
			}
		}},
		{"a discovery pass", func(rng *rand.Rand) {
			seen := map[string]struct{}{}
			for id := range fake.sessions {
				seen[id] = struct{}{}
			}
			if rng.Intn(2) == 0 {
				delete(seen, session(rng))
			}
			if _, err := reg.RecordCatalogDiscovery(surface.KindCodex, seen, f.clock, true, 3); err != nil {
				t.Fatal(err)
			}
		}},
		{"a pin or unpin", func(rng *rand.Rand) {
			id := session(rng)
			if rng.Intn(2) == 0 {
				if _, err := reg.PinSession(id, f.clock); err != nil {
					t.Fatal(err)
				}
			} else if err := reg.UnpinSession(id); err != nil {
				t.Fatal(err)
			}
		}},
		{"a snooze or wake", func(rng *rand.Rand) {
			id := session(rng)
			if rng.Intn(2) == 0 {
				_, _ = reg.SnoozeSession(id, registry.Snooze{At: f.clock, Until: f.clock.Add(time.Duration(1+rng.Intn(180)) * time.Minute), Wake: registry.SnoozeWakeTime})
			} else if _, err := reg.WakeSession(id); err != nil {
				t.Fatal(err)
			}
		}},
		{"a keep-alive attempt on a pinned session", func(rng *rand.Rand) {
			id := session(rng)
			if _, err := reg.PinSession(id, f.clock); err != nil {
				t.Fatal(err)
			}
			state := registry.KeepAlive{Attempts: 1 + rng.Intn(3), FirstAttempt: f.clock, NextAttempt: f.clock.Add(time.Minute)}
			if rng.Intn(2) == 0 {
				state.FailedAt, state.Error = f.clock, fmt.Sprint("refused ", rng.Intn(3))
			}
			if err := reg.SaveKeepAlive(id, state); err != nil {
				t.Fatal(err)
			}
		}},
		{"a lead handed over on a restart", func(rng *rand.Rand) {
			lead, err := reg.CoordinatorNamed("lead-b")
			if err != nil {
				return
			}
			previous, err := reg.CoordinatorSessionID(lead)
			if err != nil || previous == "" {
				return
			}
			if _, err := reg.HandOverCoordinator(lead, previous, session(rng), registry.CoordinatorHandover{ChangedBy: "daemon", Reason: "restarted"}); err != nil && !errors.Is(err, registry.ErrCoordinatorMoved) {
				t.Fatal(err)
			}
		}},
		{"a turn opened or ended", func(rng *rand.Rand) {
			id := session(rng)
			switch rng.Intn(3) {
			case 0:
				if err := reg.OpenTurn(id, f.clock); err != nil {
					t.Fatal(err)
				}
				return
			case 1:
				if err := reg.BindTurnCompletion(id, fmt.Sprint("turn-", rng.Intn(3))); err != nil {
					t.Fatal(err)
				}
				return
			}
			if _, err := reg.EndTurn(registry.TurnEnd{SessionID: id, Headline: fmt.Sprint("done ", rng.Intn(3)), Needs: []string{"", "a decision"}[rng.Intn(2)], Source: registry.TurnSourceTool}, "", f.clock); err != nil && !errors.Is(err, registry.ErrTurnAlreadyEnded) {
				t.Fatal(err)
			}
		}},
		{"a task worktree begun, settled or kept", func(rng *rand.Rand) {
			id := session(rng)
			path := filepath.Join(cwd, ".worktrees", id)
			switch rng.Intn(3) {
			case 0:
				_ = reg.BeginWork(registry.WorkRecord{WorktreePath: path, RepoRoot: cwd, Branch: "task/" + id, BaseRef: "main", BaseCommit: "base"}, f.clock)
				if _, err := reg.ActivateWork(path, id); err != nil {
					t.Fatal(err)
				}
			case 1:
				if _, err := reg.SettleWork(id, registry.WorkSettledPerson, f.clock); err != nil {
					t.Fatal(err)
				}
			default:
				if err := reg.SetWorkCleanup(path, registry.WorkCleanupKept, fmt.Sprint("kept ", rng.Intn(3)), nil); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"a pull request linked, unlinked or watched", func(rng *rand.Rand) {
			id, number := session(rng), 1+rng.Intn(2)
			var err error
			switch rng.Intn(3) {
			case 0:
				_, err = reg.LinkPR(registry.SessionPR{SessionID: id, Repo: "github.com/acme/app", Number: number, URL: fmt.Sprint("https://github.com/acme/app/pull/", number), Source: registry.PRSourceAgent, Title: fmt.Sprint("change ", rng.Intn(3))}, rng.Intn(2) == 0, f.clock)
			case 1:
				_, err = reg.UnlinkPR(id, "github.com/acme/app", number)
			default:
				_, err = reg.SetPRWatch(id, "github.com/acme/app", number, rng.Intn(2) == 0)
			}
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"a runtime completion", func(rng *rand.Rand) {
			if err := reg.SaveRuntimeState(session(rng), surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: fmt.Sprint("turn-", rng.Intn(3))}); err != nil {
				t.Fatal(err)
			}
		}},
		{"a paired host's row", func(rng *rand.Rand) {
			mirror("far", []surface.SessionStatus{surface.StatusIdle, surface.StatusBusy}[rng.Intn(2)])
		}},
		{"the Codex recent window", func(rng *rand.Rand) {
			if err := SaveDashboardConfig(DashboardConfig{CodexRecentHours: 1 + rng.Intn(12)}); err != nil {
				t.Fatal(err)
			}
		}},
		{"this computer's name", func(rng *rand.Rand) {
			identity := d.hostIdentity()
			identity.Name = fmt.Sprint("studio-", rng.Intn(3))
			d.setHostIdentity(identity)
		}},
		{"a restart", func(*rand.Rand) { d.presentation.epoch = uuid.NewString() }},
		{"the clock passing a refresh deadline", func(rng *rand.Rand) {
			_, state := f.read(t, sessionViewRequest{Scope: "current", Limit: 1}, false)
			if state.View != nil && state.View.RefreshAt != nil && rng.Intn(2) == 0 {
				f.clock = state.View.RefreshAt.Add(time.Second)
			} else {
				f.clock = f.clock.Add(time.Duration(rng.Intn(120)) * time.Minute)
			}
		}},
	}
	f.fill = func() {
		if err := reg.UnarchiveProject(here.ID); err != nil {
			t.Fatal(err)
		}
		if err := reg.RecordLimitStop(fake.sessions["s4"], "account", surface.LimitStop{TurnID: "fill", RefusedAt: f.clock, ResetsAt: f.clock.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.PinSession("s0", f.clock); err != nil {
			t.Fatal(err)
		}
		if err := reg.SaveKeepAlive("s0", registry.KeepAlive{Attempts: 2, FirstAttempt: f.clock, FailedAt: f.clock, Error: "refused"}); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.EndTurn(registry.TurnEnd{SessionID: "s0", Headline: "filled", Source: registry.TurnSourceTool}, "", f.clock); err != nil && !errors.Is(err, registry.ErrTurnAlreadyEnded) {
			t.Fatal(err)
		}
		if err := reg.SaveRuntimeState("s0", surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "fill"}); err != nil {
			t.Fatal(err)
		}
		if err := reg.BindTurnCompletion("s0", "fill"); err != nil {
			t.Fatal(err)
		}
		filled := registry.WorkRecord{WorktreePath: filepath.Join(cwd, ".worktrees", "filled"), RepoRoot: cwd, Branch: "task/filled", BaseRef: "main", BaseCommit: "base"}
		if err := reg.BeginWork(filled, f.clock); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.ActivateWork(filled.WorktreePath, "s5"); err != nil {
			t.Fatal(err)
		}
		if _, err := reg.LinkPR(registry.SessionPR{SessionID: "s3", Repo: "github.com/acme/app", Number: 9, URL: "https://github.com/acme/app/pull/9", Source: registry.PRSourceBranch, Title: "filled"}, false, f.clock); err != nil {
			t.Fatal(err)
		}
		if err := reg.SetAlias("filled", "s0"); err != nil {
			t.Fatal(err)
		}
		if err := reg.QueueMessage("s2", "filled"); err != nil {
			t.Fatal(err)
		}
		intent, err := reg.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "s5", TargetSessionID: "s2", Status: registry.DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.FailDeliveryIntent(intent.ID, "not delivered"); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"lead-was", "lead-only"} {
			idle := surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: cwd, LastActive: f.clock.Add(-72 * time.Hour)}
			fake.sessions[id] = idle
			if err := reg.RegisterSession(idle); err != nil {
				t.Fatal(err)
			}
		}
		for _, ended := range []bool{false, true} {
			old := surface.Session{ID: fmt.Sprint("old-ended-", ended), Name: "old", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusNotLoaded, Ended: ended, Cwd: cwd, LastActive: f.clock.Add(-96 * time.Hour)}
			fake.sessions[old.ID] = old
			if err := reg.RegisterSession(old); err != nil {
				t.Fatal(err)
			}
		}
		d.discoverCatalog(f.ctx)
		lead := registry.CoordinatorSpec{ProjectID: here.ID, ProjectRoot: cwd, Cwd: cwd, Name: "lead-fill", Surface: string(surface.KindCodex), SessionID: "lead-was", ChangedAt: f.clock}
		stored, err := reg.SetCoordinator(lead, registry.CoordinatorChange{SessionID: "lead-was", Replace: true, OverPeers: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reg.HandOverCoordinator(stored, "lead-was", "lead-only", registry.CoordinatorHandover{ChangedBy: "daemon", Reason: "restarted"}); err != nil {
			t.Fatal(err)
		}
		if err := reg.ArchiveSession("s3", f.clock); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
