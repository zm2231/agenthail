package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sqlite "modernc.org/sqlite"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type localStatusSurface struct {
	*daemonSurface
	dir         string
	statusCalls atomic.Int32
	duringList  func()
}

func (s *localStatusSurface) List(ctx context.Context) ([]surface.Session, error) {
	sessions, err := s.daemonSurface.List(ctx)
	for index := range sessions {
		if local, localErr := s.LocalStatus(ctx, sessions[index]); localErr == nil {
			sessions[index] = local
		}
	}
	if s.duringList != nil {
		s.duringList()
	}
	return sessions, err
}

func (s *localStatusSurface) statusPath(id string) string {
	return filepath.Join(s.dir, id+".status")
}

func (s *localStatusSurface) LocalStatusFiles(session surface.Session) []string {
	return []string{s.statusPath(session.ID)}
}

func (s *localStatusSurface) LocalStatus(_ context.Context, session surface.Session) (surface.Session, error) {
	s.statusCalls.Add(1)
	raw, err := os.ReadFile(s.statusPath(session.ID))
	if err != nil {
		return session, err
	}
	session.Status = surface.SessionStatus(strings.TrimSpace(string(raw)))
	return session, nil
}

func (s *localStatusSurface) writeStatus(t *testing.T, id string, status surface.SessionStatus) {
	t.Helper()
	path := s.statusPath(id)
	previous, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte(status+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if previous != nil {
		// Same-size rewrites inside one timestamp tick must still read as a change.
		bumped := previous.ModTime().Add(time.Second)
		if err := os.Chtimes(path, bumped, bumped); err != nil {
			t.Fatal(err)
		}
	}
}

func localStatusFixture(t *testing.T) (*Daemon, *registry.Registry, *localStatusSurface) {
	t.Helper()
	d, store, fake, _, _ := daemonFixture(t)
	source := &localStatusSurface{daemonSurface: fake, dir: t.TempDir()}
	for id, session := range fake.sessions {
		source.writeStatus(t, id, session.Status)
	}
	d.Surfaces = []surface.Surface{source}
	return d, store, source
}

func catalogStatusEvents(t *testing.T, store *registry.Registry, after uint64, sessionID string) []string {
	t.Helper()
	window, err := store.CatalogEventsAfter(after, 100)
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, event := range window.Events {
		if event.Type != "session.upserted" || event.EntityID != sessionID {
			continue
		}
		var envelope struct {
			Session struct {
				Status string `json:"status"`
			} `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, envelope.Session.Status)
	}
	return statuses
}

func latestCatalogSeq(t *testing.T, store *registry.Registry) uint64 {
	t.Helper()
	_, latest, err := store.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	return latest
}

func TestStatusPassPublishesTransitionsWithoutDiscovery(t *testing.T) {
	d, store, source := localStatusFixture(t)
	d.discoverCatalog(context.Background())
	listCalls := source.listCalls.Load()
	after := latestCatalogSeq(t, store)

	d.refreshCatalogStatus(context.Background())
	if statuses := catalogStatusEvents(t, store, after, "from"); len(statuses) != 0 {
		t.Fatalf("unchanged status published %v", statuses)
	}

	source.writeStatus(t, "from", surface.StatusBusy)
	d.refreshCatalogStatus(context.Background())
	if statuses := catalogStatusEvents(t, store, after, "from"); len(statuses) != 1 || statuses[0] != string(surface.StatusBusy) {
		t.Fatalf("busy transition events=%v", statuses)
	}
	if statuses := catalogStatusEvents(t, store, after, "to"); len(statuses) != 0 {
		t.Fatalf("untouched session published %v", statuses)
	}

	calls := source.statusCalls.Load()
	d.refreshCatalogStatus(context.Background())
	if source.statusCalls.Load() != calls {
		t.Fatalf("status re-derived without a file change")
	}

	source.writeStatus(t, "from", surface.StatusIdle)
	d.refreshCatalogStatus(context.Background())
	if statuses := catalogStatusEvents(t, store, after, "from"); len(statuses) != 2 || statuses[1] != string(surface.StatusIdle) {
		t.Fatalf("idle transition events=%v", statuses)
	}
	if source.listCalls.Load() != listCalls {
		t.Fatalf("status pass called provider List")
	}
	snapshot, err := store.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range snapshot.Sessions {
		if record.Session.ID == "from" && record.Session.Status != surface.StatusIdle {
			t.Fatalf("snapshot status=%s", record.Session.Status)
		}
	}
}

func TestStatusPassRefreshesEvidenceThatChangedDuringDiscovery(t *testing.T) {
	d, store, source := localStatusFixture(t)
	d.discoverCatalog(context.Background())
	d.refreshCatalogStatus(context.Background())
	after := latestCatalogSeq(t, store)
	// The transcript records the next turn after List read it.
	source.duringList = func() { source.writeStatus(t, "from", surface.StatusBusy) }
	d.discoverCatalog(context.Background())
	source.duringList = nil
	if statuses := catalogStatusEvents(t, store, after, "from"); len(statuses) != 0 {
		t.Fatalf("discovery published %v", statuses)
	}
	d.refreshCatalogStatus(context.Background())
	if statuses := catalogStatusEvents(t, store, after, "from"); len(statuses) != 1 || statuses[0] != string(surface.StatusBusy) {
		t.Fatalf("events=%v", statuses)
	}
}

func TestDiscoveryKeepsLocalStatusTheProviderHasNotCaughtUpWith(t *testing.T) {
	d, store, source := localStatusFixture(t)
	d.discoverCatalog(context.Background())
	d.refreshCatalogStatus(context.Background())
	after := latestCatalogSeq(t, store)
	source.writeStatus(t, "from", surface.StatusBusy)
	d.refreshCatalogStatus(context.Background())
	if source.sessions["from"].Status != surface.StatusIdle {
		t.Fatal("provider fixture must still report idle")
	}
	d.discoverCatalog(context.Background())
	d.refreshCatalogStatus(context.Background())
	if statuses := catalogStatusEvents(t, store, after, "from"); len(statuses) != 1 || statuses[0] != string(surface.StatusBusy) {
		t.Fatalf("events=%v want only the busy transition", statuses)
	}
}

func TestStatusPassStopsFollowingFailedSurface(t *testing.T) {
	d, store, source := localStatusFixture(t)
	d.discoverCatalog(context.Background())
	d.Surfaces = []surface.Surface{&failingClaudeListSurface{daemonSurface: source.daemonSurface}}
	d.discoverCatalog(context.Background())
	if len(d.catalogLive) != 0 {
		t.Fatalf("live sessions=%d after failed discovery", len(d.catalogLive))
	}
	after := latestCatalogSeq(t, store)
	source.writeStatus(t, "from", surface.StatusBusy)
	d.refreshCatalogStatus(context.Background())
	if statuses := catalogStatusEvents(t, store, after, "from"); len(statuses) != 0 {
		t.Fatalf("stale row republished as fresh: %v", statuses)
	}
}

func TestRunCatalogPublishesStatusWithinTwoSeconds(t *testing.T) {
	d, store, source := localStatusFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.runCatalog(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(catalogStatusEvents(t, store, 0, "from")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("initial discovery did not publish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Let the first status tick record the post-discovery stamps.
	time.Sleep(catalogStatusInterval + 100*time.Millisecond)
	window, events, unsubscribe, err := d.catalog.subscribe(latestCatalogSeq(t, store))
	if err != nil || window.Gap {
		t.Fatalf("subscribe gap=%v err=%v", window.Gap, err)
	}
	defer unsubscribe()
	started := time.Now()
	source.writeStatus(t, "from", surface.StatusBusy)
	timeout := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "session.upserted" || event.EntityID != "from" || !strings.Contains(string(event.Payload), `"status":"busy"`) {
				continue
			}
			t.Logf("status latency=%s", time.Since(started).Round(time.Millisecond))
			return
		case <-timeout:
			t.Fatal("busy status did not reach the catalog stream within 2s")
		}
	}
}

var (
	commitHookOnce sync.Once
	commitCounters sync.Map
)

// countCatalogCommits must run before the registry at path is opened.
func countCatalogCommits(path string) *atomic.Int64 {
	commitHookOnce.Do(func() {
		sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
			counter, found := commitCounters.Load(strings.SplitN(dsn, "?", 2)[0])
			hooks, ok := conn.(sqlite.HookRegisterer)
			if found && ok {
				hooks.RegisterCommitHook(func() int32 {
					counter.(*atomic.Int64).Add(1)
					return 0
				})
			}
			return nil
		})
	})
	counter := &atomic.Int64{}
	commitCounters.Store(path, counter)
	return counter
}

func TestUnchangedDiscoveryPassWritesNoSessionRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	commits := countCatalogCommits(path)
	store, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	fake := &daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	for index := 0; index < 20; index++ {
		id := fmt.Sprintf("session-%02d", index)
		fake.sessions[id] = surface.Session{ID: id, Surface: surface.KindCodex, Status: surface.StatusIdle, Cwd: t.TempDir(), Source: "vscode", Transport: "desktop", LastActive: time.UnixMilli(1_790_000_000_000)}
	}
	d := New(store, []surface.Surface{fake})
	d.discoverCatalog(context.Background())
	first, err := store.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	before := commits.Load()
	d.discoverCatalog(context.Background())
	writes := commits.Load() - before
	t.Logf("write transactions in an unchanged pass over %d sessions: %d", len(fake.sessions), writes)
	// One transaction closes the surface's discovery and one records surface health.
	if writes != 2 {
		t.Fatalf("write transactions=%d want 2", writes)
	}
	second, err := store.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if second.CatalogSeq != first.CatalogSeq || len(second.Sessions) != len(first.Sessions) {
		t.Fatalf("seq %d->%d sessions %d->%d", first.CatalogSeq, second.CatalogSeq, len(first.Sessions), len(second.Sessions))
	}
	prior := map[string]registry.CatalogSessionState{}
	for _, record := range first.Sessions {
		prior[record.Session.ID] = record
	}
	for _, record := range second.Sessions {
		was := prior[record.Session.ID]
		if record.Freshness.Generation != was.Freshness.Generation || record.Freshness.Stale {
			t.Fatalf("%s freshness %+v -> %+v", record.Session.ID, was.Freshness, record.Freshness)
		}
		if !record.ObservedAt.After(was.ObservedAt) || !record.Freshness.ObservedAt.Equal(record.ObservedAt) {
			t.Fatalf("%s observedAt %s -> %s did not advance", record.Session.ID, was.ObservedAt, record.ObservedAt)
		}
	}

	changed := fake.sessions["session-03"]
	changed.Status = surface.StatusBusy
	fake.sessions["session-03"] = changed
	before = commits.Load()
	d.discoverCatalog(context.Background())
	if writes := commits.Load() - before; writes != 3 {
		t.Fatalf("write transactions with one changed session=%d want 3", writes)
	}
}

func TestCatalogIdentityResolvesPathWithNewline(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "line\nbreak")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-b", "main")
	identity := newCatalogIdentityCache().identity(context.Background(), surface.Session{Cwd: repo}, time.Now())
	if identity.UnavailableReason != "" || identity.HostProject.CommonDir == "" || filepath.Base(identity.Checkout.Path) != "line\nbreak" {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestCatalogIdentityCacheReusesResolvedCheckout(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "README.md")
	gitRun(t, repo, "commit", "-m", "base")
	calls := countGitCalls(t)
	cache := newCatalogIdentityCache()
	now := time.Now()
	session := surface.Session{Cwd: repo}

	first := cache.identity(context.Background(), session, now)
	resolved := calls()
	if resolved == 0 || first.Checkout.Branch != "main" || first.Checkout.Dirty {
		t.Fatalf("calls=%d identity=%+v", resolved, first.Checkout)
	}
	if again := cache.identity(context.Background(), session, now.Add(time.Minute)); again != first || calls() != resolved {
		t.Fatalf("cached lookup ran %d git commands", calls()-resolved)
	}

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("edited\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if edited := cache.identity(context.Background(), session, now.Add(time.Minute)); edited.Checkout.Dirty || calls() != resolved {
		t.Fatalf("working tree edit re-resolved before expiry: dirty=%v calls=%d", edited.Checkout.Dirty, calls()-resolved)
	}
	if expired := cache.identity(context.Background(), session, now.Add(catalogIdentityTTL)); !expired.Checkout.Dirty || calls() == resolved {
		t.Fatalf("expired entry was reused: %+v", expired.Checkout)
	}

	gitRun(t, repo, "checkout", "-q", "-b", "feature/cache")
	switched := calls()
	if branch := cache.identity(context.Background(), session, now.Add(catalogIdentityTTL)); branch.Checkout.Branch != "feature/cache" || calls() == switched {
		t.Fatalf("branch switch did not invalidate: %+v", branch.Checkout)
	}
}

func countGitCalls(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\nexec %q \"$@\"\n", logPath, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		raw, _ := os.ReadFile(logPath)
		return strings.Count(string(raw), "\n")
	}
}
