package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func latestCatalogRow(t *testing.T, r *registry.Registry, sessionID string) dashboardSession {
	t.Helper()
	window, err := r.CatalogEventsAfter(0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for index := len(window.Events) - 1; index >= 0; index-- {
		event := window.Events[index]
		if event.Type != "session.upserted" || event.EntityID != sessionID {
			continue
		}
		var envelope struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Session
	}
	t.Fatalf("no catalog row for %s", sessionID)
	return dashboardSession{}
}

func TestARowLaterInADiscoveryPassShowsWhatChangedAfterThePassBegan(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	view, err := d.loadCatalogViewState()
	if err != nil {
		t.Fatal(err)
	}
	rows := newCatalogRowSource([]surface.Session{from, to})
	publish := func(session surface.Session) {
		t.Helper()
		if !d.publishCatalogSession(context.Background(), fake, session, nil, projectresolve.CatalogIdentity{}, "", 0, nil, false, dashconfig.Config{}, view, rows, time.Now().UTC(), nil, nil) {
			t.Fatalf("%s was not published", session.ID)
		}
	}
	publish(from)
	if err := r.RecordSessionOrigin(registry.OriginOf(to.ID, from.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateTask(from.ID, to.ID, "review", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LinkPR(registry.SessionPR{SessionID: to.ID, Repo: "acme/app", Number: 7, URL: "https://github.com/acme/app/pull/7", Source: "explicit"}, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	publish(to)
	row := latestCatalogRow(t, r, to.ID)
	if row.StartedBy != from.ID || !row.Task || len(row.PRs) != 1 {
		t.Fatalf("row published after the pass began shows startedBy=%q task=%v prs=%d, want %q, a task and one pull request", row.StartedBy, row.Task, len(row.PRs), from.ID)
	}
}

// A discovery pass republishes every listed row; reading each row's
// metadata with its own queries made an unchanged pass of a thousand rows
// cost about 440 allocations a row.
func TestAnUnchangedDiscoveryPassReadsRowMetadataOncePerPass(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fake := &daemonSurface{}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	sessions := make([]surface.Session, 64)
	for index := range sessions {
		sessions[index] = surface.Session{ID: fmt.Sprintf("listed-%d", index), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: "desktop", Status: surface.StatusOffline}
		if err := r.RecordObservedSession(sessions[index]); err != nil {
			t.Fatal(err)
		}
	}
	pass := func() {
		rows := newCatalogRowSource(sessions)
		for _, session := range sessions {
			if !d.publishCatalogSession(context.Background(), fake, session, nil, projectresolve.CatalogIdentity{}, "", 0, nil, false, dashconfig.Config{}, catalogViewState{}, rows, time.Now(), nil, nil) {
				t.Fatalf("%s was not published", session.ID)
			}
		}
	}
	pass()
	_, before, err := r.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	perRow := testing.AllocsPerRun(3, pass) / float64(len(sessions))
	if _, after, err := r.CatalogState(); err != nil || after != before {
		t.Fatalf("an unchanged pass appended catalog events: %d -> %d, err=%v", before, after, err)
	}
	if perRow > 300 {
		t.Fatalf("an unchanged pass allocates %.0f times a row, want at most 300", perRow)
	}
}

type writeDuringRowLoad struct {
	*registry.Registry
	write func()
}

func (s *writeDuringRowLoad) CatalogRowMetadata(sessions []string) (registry.CatalogRowMetadata, error) {
	meta, err := s.Registry.CatalogRowMetadata(sessions)
	if write := s.write; write != nil {
		s.write = nil
		write()
	}
	return meta, err
}

func TestARowIsNotServedFromAReadThatAWriteInterrupted(t *testing.T) {
	_, r, _, from, to := daemonFixture(t)
	store := &writeDuringRowLoad{Registry: r, write: func() {
		if _, err := r.LinkPR(registry.SessionPR{SessionID: from.ID, Repo: "acme/app", Number: 9, URL: "https://github.com/acme/app/pull/9", Source: "explicit"}, true, time.Now()); err != nil {
			t.Fatal(err)
		}
	}}
	rows := newCatalogRowSource([]surface.Session{from, to})
	row, err := rows.row(store, from.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.PRs) != 1 {
		t.Fatalf("row read while a pull request was linked shows %d pull requests, want the one committed before it was served", len(row.PRs))
	}
}
