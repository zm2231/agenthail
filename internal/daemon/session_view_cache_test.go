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

func TestCachedSessionViewsMatchUncachedAfterEveryKindOfWrite(t *testing.T) {
	d, reg, fake, _, _, cwd := baseFixture(t)
	ctx := context.Background()
	clock := time.Now().UTC()
	d.presentation.clock = func() time.Time { return clock }
	handler := d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	hostID := "h0123456789ab"
	if _, err := reg.AddHost(registry.PairedHost{ID: hostID, Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	mirror := func(id string, status surface.SessionStatus) {
		session := surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: status}
		raw, _ := json.Marshal(dashboardSession{ID: id, Name: id, Surface: session.Surface, Status: status, Host: &dashboardHost{ID: hostID, Name: "peer", State: "connected"}})
		if _, _, err := reg.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: session, EntityID: hostID + "::" + id, Projection: raw, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: clock}); err != nil {
			t.Fatal(err)
		}
	}
	mirror("far", surface.StatusIdle)
	here := registry.ProjectRecord{ID: "project-here", WorkspaceRoot: cwd, Title: "here", Added: true, CreatedAt: clock}
	if err := reg.AddProject(here); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		session := surface.Session{ID: fmt.Sprint("s", i), Name: fmt.Sprint("s", i), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: cwd, LastActive: clock.Add(-time.Duration(i*7) * time.Hour)}
		if i == 5 {
			session.Subagent = &surface.Subagent{ParentID: "s4", RootID: "s4"}
		}
		fake.sessions[session.ID] = session
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(ctx)

	requests := []sessionViewRequest{
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
	read := func(request sessionViewRequest, uncached bool) (string, SessionViewSnapshot) {
		t.Helper()
		d.presentation.uncached = uncached
		defer func() { d.presentation.uncached = false }()
		state, err := d.readSessionView(ctx, request)
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
	compare := func(step string) {
		t.Helper()
		for _, request := range requests {
			cached, state := read(request, false)
			plain, _ := read(request, true)
			if cached != plain {
				t.Fatalf("after %s, %s %+v differs:\ncached   %s\nuncached %s", step, request.Scope, request.Filters, cached, plain)
			}
			if state.NextCursor != "" {
				next := request
				cursor, ok := decodeSessionViewCursor(state.NextCursor)
				if !ok {
					t.Fatal("bad cursor")
				}
				next.After = &cursor.After
				if cachedPage, plainPage := first(read(next, false)), first(read(next, true)); cachedPage != plainPage {
					t.Fatalf("after %s, the next page differs", step)
				}
			}
		}
	}

	session := func(rng *rand.Rand) string { return fmt.Sprint("s", rng.Intn(5)) }
	var intents []int64
	writes := []struct {
		name string
		do   func(*rand.Rand)
	}{
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
			if message, err := reg.ClaimNextMessage(id, clock); err == nil && message != nil {
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
				_ = reg.ArchiveSession(id, clock)
			} else {
				_ = reg.UnarchiveSession(id)
			}
		}},
		{"a project", func(rng *rand.Rand) {
			project := registry.ProjectRecord{ID: fmt.Sprint("project-", rng.Intn(3)), WorkspaceRoot: filepath.Join(t.TempDir(), "work"), Title: "work", Added: true, CreatedAt: clock}
			_ = reg.AddProject(project)
			if rng.Intn(2) == 0 {
				_ = reg.ArchiveProject(project, clock)
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
				if err := reg.RecordLimitStop(fake.sessions[id], "account", surface.LimitStop{TurnID: "turn", RefusedAt: clock, ResetsAt: clock.Add(time.Hour)}); err != nil {
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
			changed.LastActive = clock.Add(-time.Duration(rng.Intn(48)) * time.Hour)
			fake.sessions[id] = changed
			if err := reg.RegisterSession(changed); err != nil {
				t.Fatal(err)
			}
			d.discoverCatalog(ctx)
		}},
		{"attention reconciled on its own", func(*rand.Rand) {
			if err := reg.ReconcileAttentionItems(clock); err != nil {
				t.Fatal(err)
			}
		}},
		{"the sessions' project archived or restored", func(rng *rand.Rand) {
			if rng.Intn(2) == 0 {
				_ = reg.ArchiveProject(here, clock)
			} else {
				_ = reg.UnarchiveProject(here.ID)
			}
		}},
		{"a lead set outside the daemon", func(rng *rand.Rand) {
			spec := registry.CoordinatorSpec{ProjectID: here.ID, ProjectRoot: cwd, Cwd: cwd, Name: "lead-b", Surface: string(surface.KindCodex), SessionID: session(rng), ChangedAt: clock}
			var taken registry.AliasTakenError
			var named registry.CoordinatorNameTakenError
			if _, err := reg.SetCoordinator(spec, registry.CoordinatorChange{SessionID: spec.SessionID, Replace: true}); err != nil && !errors.As(err, &taken) && !errors.As(err, &named) {
				t.Fatal(err)
			}
		}},
		{"a session row with no catalog event", func(rng *rand.Rand) {
			changed := fake.sessions[session(rng)]
			changed.Status = []surface.SessionStatus{surface.StatusIdle, surface.StatusBusy}[rng.Intn(2)]
			changed.LastActive = clock.Add(-time.Duration(rng.Intn(48)) * time.Hour)
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
			if _, err := reg.RecordCatalogDiscovery(surface.KindCodex, seen, clock, true, 3); err != nil {
				t.Fatal(err)
			}
		}},
		{"a pin or unpin", func(rng *rand.Rand) {
			id := session(rng)
			if rng.Intn(2) == 0 {
				if _, err := reg.PinSession(id, clock); err != nil {
					t.Fatal(err)
				}
			} else if err := reg.UnpinSession(id); err != nil {
				t.Fatal(err)
			}
		}},
		{"a snooze or wake", func(rng *rand.Rand) {
			id := session(rng)
			if rng.Intn(2) == 0 {
				_, _ = reg.SnoozeSession(id, registry.Snooze{At: clock, Until: clock.Add(time.Duration(1+rng.Intn(180)) * time.Minute), Wake: registry.SnoozeWakeTime})
			} else if _, err := reg.WakeSession(id); err != nil {
				t.Fatal(err)
			}
		}},
		{"a keep-alive attempt", func(rng *rand.Rand) {
			state := registry.KeepAlive{Attempts: 1 + rng.Intn(3), FirstAttempt: clock, NextAttempt: clock.Add(time.Minute)}
			if rng.Intn(2) == 0 {
				state.FailedAt, state.Error = clock, fmt.Sprint("refused ", rng.Intn(3))
			}
			if err := reg.SaveKeepAlive(session(rng), state); err != nil {
				t.Fatal(err)
			}
		}},
		{"a turn opened or ended", func(rng *rand.Rand) {
			id := session(rng)
			switch rng.Intn(3) {
			case 0:
				if err := reg.OpenTurn(id, clock); err != nil {
					t.Fatal(err)
				}
				return
			case 1:
				if err := reg.BindTurnCompletion(id, fmt.Sprint("turn-", rng.Intn(3))); err != nil {
					t.Fatal(err)
				}
				return
			}
			if _, err := reg.EndTurn(registry.TurnEnd{SessionID: id, Headline: fmt.Sprint("done ", rng.Intn(3)), Needs: []string{"", "a decision"}[rng.Intn(2)], Source: registry.TurnSourceTool}, "", clock); err != nil && !errors.Is(err, registry.ErrTurnAlreadyEnded) {
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
			_, state := read(sessionViewRequest{Scope: "current", Limit: 1}, false)
			if state.View != nil && state.View.RefreshAt != nil && rng.Intn(2) == 0 {
				clock = state.View.RefreshAt.Add(time.Second)
			} else {
				clock = clock.Add(time.Duration(rng.Intn(120)) * time.Minute)
			}
		}},
	}

	compare("the first read")
	hitsBefore := d.presentation.hits
	compare("nothing")
	if d.presentation.hits == hitsBefore {
		t.Fatal("a read with nothing changed did not use the cache")
	}
	rng := rand.New(rand.NewSource(20261106))
	for step := range 400 {
		write := writes[rng.Intn(len(writes))]
		write.do(rng)
		compare(fmt.Sprintf("step %d, %s", step, write.name))
	}
	t.Logf("cache hits %d, builds %d", d.presentation.hits, d.presentation.builds)
}

func first(encoded string, _ SessionViewSnapshot) string { return encoded }

func TestASessionViewBuiltAcrossALeadChangeIsNotReused(t *testing.T) {
	d, reg, fake, _, _, cwd := baseFixture(t)
	ctx := context.Background()
	here := registry.ProjectRecord{ID: "project-here", WorkspaceRoot: cwd, Title: "here", Added: true, CreatedAt: time.Now()}
	if err := reg.AddProject(here); err != nil {
		t.Fatal(err)
	}
	old := surface.Session{ID: "old", Name: "old", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: cwd, LastActive: time.Now().Add(-72 * time.Hour)}
	fake.sessions[old.ID] = old
	if err := reg.RegisterSession(old); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(ctx)
	request := sessionViewRequest{Scope: "current", Limit: 200, Members: true}
	read := func(uncached bool) (string, bool) {
		t.Helper()
		d.presentation.uncached = uncached
		defer func() { d.presentation.uncached = false }()
		state, err := d.readSessionView(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		shown := false
		for _, row := range state.Sessions {
			shown = shown || row.ID == old.ID
		}
		state.PresentationPosition.Sequence = 0
		state.View.AsOf = time.Time{}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded), shown
	}
	if _, shown := read(false); shown {
		t.Fatal("an old idle session that leads nothing is current")
	}
	d.presentation.building = func() {
		d.presentation.building = nil
		spec := registry.CoordinatorSpec{ProjectID: here.ID, ProjectRoot: cwd, Cwd: cwd, Name: "lead-old", Surface: string(surface.KindCodex), SessionID: old.ID, ChangedAt: time.Now()}
		if _, err := reg.SetCoordinator(spec, registry.CoordinatorChange{SessionID: old.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.SetAlias("other", "from"); err != nil {
		t.Fatal(err)
	}
	builds := d.presentation.builds
	read(false)
	if d.presentation.building != nil || d.presentation.builds != builds+1 {
		t.Fatal("the lead did not change during a cached build")
	}
	cached, shown := read(false)
	plain, _ := read(true)
	if cached != plain || !shown {
		t.Fatalf("a view built while the lead changed was reused:\ncached   %s\nuncached %s", cached, plain)
	}
}
