package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCachedSessionViewsMatchUncachedAfterEveryKindOfWrite(t *testing.T) {
	f := newViewFixture(t)
	compare := func(step string) {
		t.Helper()
		stored := f.stored(t)
		defer stored.Close()
		for _, request := range f.requests {
			cached, state := f.read(t, request, nil)
			plain, _ := f.read(t, request, stored)
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
				if cachedPage, _ := f.read(t, next, nil); cachedPage != first(f.read(t, next, stored)) {
					t.Fatalf("after %s, the next page differs", step)
				}
			}
		}
	}
	compare("the first read")
	hitsBefore := f.d.presentation.hits
	compare("nothing")
	if f.d.presentation.hits == hitsBefore {
		t.Fatal("a read with nothing changed did not use the cache")
	}
	f.populateRounds(rand.New(rand.NewSource(20261106)), 4, func(step int, write string) {
		compare(fmt.Sprintf("step %d, %s", step, write))
	})
	t.Logf("cache hits %d, builds %d", f.d.presentation.hits, f.d.presentation.builds)
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
