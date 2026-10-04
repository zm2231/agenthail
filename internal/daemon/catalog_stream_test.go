package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type boundedCatalogSurface struct{ *daemonSurface }

type blockingObserveSurface struct {
	*daemonSurface
	entered chan struct{}
	release <-chan struct{}
}

func (s *blockingObserveSurface) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.daemonSurface.Observe(ctx, session)
}

func (boundedCatalogSurface) CatalogListComplete() bool { return false }

func (s *boundedCatalogSurface) List(ctx context.Context) ([]surface.Session, error) {
	sessions, err := s.daemonSurface.List(ctx)
	if len(sessions) > 50 {
		sessions = sessions[:50]
	}
	return sessions, err
}

func TestAPICatalogStreamReplaysPersistedEvent(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	if _, created, err := d.catalog.publish(registry.CatalogEvent{DedupeKey: "session:from:1", Type: "session.upserted", EntityID: "from", Payload: []byte(`{"id":"from"}`)}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/api/v1/catalog-events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"stream":"catalog"`) || !strings.Contains(line, `"type":"session.upserted"`) {
				t.Fatalf("line=%q", line)
			}
			break
		}
	}
}

func TestActiveCatalogSubscriberReceivesTerminalDeliveryProblem(t *testing.T) {
	d, store, fake, sender, target := daemonFixture(t)
	if _, _, err := store.QueueDeliveryWithIntent(target.ID, "deliver later", "", surface.SendOptions{SourceSessionID: sender.ID}); err != nil {
		t.Fatal(err)
	}
	reader, closeStream := openAuthorizedCatalogStream(t, d)
	defer closeStream()
	fake.sendErr = surface.DeliveryTerminal(fmt.Errorf("target rejected input"), surface.DeliveryInvalidRequest)
	d.drainMessageQueue(context.Background(), fake, &target)
	event := receiveCatalogSSE(t, reader)
	if event.Type != "delivery.problem" {
		t.Fatalf("event=%+v", event)
	}
}

func TestActiveCatalogSubscriberReceivesExpiredDeliveryProblemOnce(t *testing.T) {
	d, store, _, sender, target := daemonFixture(t)
	if _, _, err := store.QueueDeliveryWithIntent(target.ID, "deliver later", "expiry", surface.SendOptions{SourceSessionID: sender.ID}); err != nil {
		t.Fatal(err)
	}
	_, events, cancel, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, err := store.ExpireMessages(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.catalog.flushCommitted(); err != nil {
		t.Fatal(err)
	}
	event := receiveCatalogEvent(t, events)
	if event.Type != "delivery.problem" {
		t.Fatalf("event=%+v", event)
	}
	if _, err := store.ExpireMessages(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.catalog.flushCommitted(); err != nil {
		t.Fatal(err)
	}
	ensureNoCatalogEvent(t, events)
}

func TestCatalogScanDrainsDeliveryProblemExpiredByQueueCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	store, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sender := surface.Session{ID: "sender", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	target := surface.Session{ID: "target", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	for _, session := range []surface.Session{sender, target} {
		if err := store.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	release := make(chan struct{})
	fake := &blockingObserveSurface{daemonSurface: &daemonSurface{sessions: map[string]surface.Session{sender.ID: sender, target.ID: target}, observations: map[string]*surface.TurnObservation{}, accepted: true}, entered: make(chan struct{}, 1), release: release}
	d := New(store, []surface.Surface{fake})
	if _, err := store.AddRoute(sender.ID, target.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	queueID, _, err := store.QueueDeliveryWithIntent(target.ID, "deliver later", "queue-count", surface.SendOptions{SourceSessionID: sender.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, events, cancel, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE message_queue SET expires_at_ms=1 WHERE id=?`, queueID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if count := store.QueueCount(target.ID); count != 0 {
		t.Fatalf("queue count=%d", count)
	}
	done := make(chan struct{})
	go func() {
		d.scanAndRelay(context.Background())
		close(done)
	}()
	event := receiveCatalogEvent(t, events)
	if event.Type != "delivery.problem" {
		t.Fatalf("event=%+v", event)
	}
	select {
	case <-fake.entered:
	case <-time.After(time.Second):
		t.Fatal("provider observation did not begin")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scan did not finish after provider observation released")
	}
}

func TestActiveCatalogSubscriberReceivesTerminalRelayProblemAndReconnectsWithoutReplay(t *testing.T) {
	d, store, fake, sender, target := daemonFixture(t)
	if _, err := store.AddRoute(sender.ID, target.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	_, events, cancel, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	d.fireRelays(&sender, "relay-turn", 0, "forwarded reply")
	fake.sendErr = surface.DeliveryTerminal(fmt.Errorf("target rejected input"), surface.DeliveryInvalidRequest)
	d.drainMessageQueue(context.Background(), fake, &target)
	first := receiveCatalogEvent(t, events)
	if first.Type != "delivery.problem" {
		t.Fatalf("first=%+v", first)
	}
	cancel()
	window, resumed, stop, err := d.catalog.subscribe(first.Seq)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if len(window.Events) != 0 {
		t.Fatalf("replayed=%+v", window.Events)
	}
	if _, created, err := d.catalog.publish(registry.CatalogEvent{DedupeKey: "session:sender:after-problem", Type: "session.upserted", EntityID: sender.ID, Payload: []byte(`{"id":"sender"}`)}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	next := receiveCatalogEvent(t, resumed)
	if next.Seq != first.Seq+1 || next.Type != "session.upserted" {
		t.Fatalf("first=%+v next=%+v", first, next)
	}
}

func TestActiveCatalogSubscriberReceivesReplyForwardFailureOnce(t *testing.T) {
	d, store, _, sender, target := daemonFixture(t)
	routeID, err := store.AddRoute(sender.ID, target.ID, ".*")
	if err != nil {
		t.Fatal(err)
	}
	_, events, cancel, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	route := registry.RouteRow{ID: routeID, FromSession: sender.ID, ToSession: target.ID, Pattern: ".*", Active: true}
	d.dropRelay(sender.ID, route, "reply-turn", "reply body", "target surface unavailable")
	first := receiveCatalogEvent(t, events)
	if first.Type != "delivery.problem" {
		t.Fatalf("first=%+v", first)
	}
	d.dropRelay(sender.ID, route, "reply-turn", "reply body", "target surface unavailable")
	ensureNoCatalogEvent(t, events)
}

func TestCatalogHubClosesStaleSubscribersAndRecoversAfterJournalGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-gap.db")
	store, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := New(store, nil)
	_, stale, cancel, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	appendCatalogGapFixture(t, path, catalogStreamReplayLimit+1)
	if err := d.catalog.flushCommitted(); err == nil {
		t.Fatal("catalog journal gap did not report an error")
	}
	if _, open := <-stale; open {
		t.Fatal("stale subscriber remained open after catalog journal gap")
	}
	staleWindow, _, stopStale, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer stopStale()
	if !staleWindow.Gap {
		t.Fatalf("stale replay window=%+v", staleWindow)
	}
	window, events, stop, err := d.catalog.subscribe(staleWindow.LatestSeq)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if window.Gap || len(window.Events) != 0 {
		t.Fatalf("reconnected window=%+v", window)
	}
	if _, created, err := store.AppendCatalogEvent(registry.CatalogEvent{DedupeKey: "gap:next", Type: "session.upserted", EntityID: "to", Payload: []byte(`{"id":"to"}`)}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if err := d.catalog.flushCommitted(); err != nil {
		t.Fatal(err)
	}
	next := receiveCatalogEvent(t, events)
	if next.Seq != staleWindow.LatestSeq+1 || next.EntityID != "to" {
		t.Fatalf("latest=%d next=%+v", staleWindow.LatestSeq, next)
	}
}

func receiveCatalogEvent(t *testing.T, events <-chan registry.CatalogEvent) registry.CatalogEvent {
	t.Helper()
	select {
	case event, open := <-events:
		if !open {
			t.Fatal("catalog subscriber closed")
		}
		return event
	case <-time.After(time.Second):
		t.Fatal("catalog subscriber did not receive an event")
		return registry.CatalogEvent{}
	}
}

func ensureNoCatalogEvent(t *testing.T, events <-chan registry.CatalogEvent) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected catalog event=%+v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func openAuthorizedCatalogStream(t *testing.T, d *Daemon) (*bufio.Reader, func()) {
	t.Helper()
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/catalog-events?after=0", nil)
	if err != nil {
		server.Close()
		cancel()
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		server.Close()
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		server.Close()
		cancel()
		t.Fatalf("status=%d", response.StatusCode)
	}
	return bufio.NewReader(response.Body), func() {
		cancel()
		response.Body.Close()
		server.Close()
	}
}

func receiveCatalogSSE(t *testing.T, reader *bufio.Reader) catalogStreamEnvelope {
	t.Helper()
	result := make(chan catalogStreamEnvelope, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event catalogStreamEnvelope
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil {
				result <- event
			}
			return
		}
	}()
	select {
	case event := <-result:
		return event
	case <-time.After(time.Second):
		t.Fatal("catalog SSE subscriber did not receive an event")
		return catalogStreamEnvelope{}
	}
}

func appendCatalogGapFixture(t *testing.T, path string, count int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at) VALUES(?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	defer statement.Close()
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	for index := 0; index < count; index++ {
		if _, err := statement.Exec(fmt.Sprintf("gap:%d", index), "session.upserted", "from", []byte(`{"id":"from"}`), createdAt); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM catalog_events WHERE seq NOT IN (SELECT seq FROM catalog_events ORDER BY seq DESC LIMIT ?)`, catalogStreamReplayLimit); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryPersistsCatalogBeforeSnapshotReads(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.listCalls.Store(0)
	d.discoverCatalog(context.Background())
	if fake.listCalls.Load() != 1 {
		t.Fatalf("discovery calls=%d", fake.listCalls.Load())
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.HostEpoch == "" || state.CatalogSeq == 0 || len(state.Sessions) != 2 || fake.listCalls.Load() != 1 {
		t.Fatalf("state=%+v providerCalls=%d", state, fake.listCalls.Load())
	}
	for _, session := range state.Sessions {
		if session.ObservedAt.IsZero() || session.UnavailableReason == "" {
			t.Fatalf("session=%+v", session)
		}
	}
	if len(state.Surfaces) != 1 || state.Surfaces[0].Health != "healthy" || !state.Surfaces[0].Connected {
		t.Fatalf("surfaces=%+v", state.Surfaces)
	}
}

func TestFailedDiscoveryKeepsCatalogRowsStaleThroughDashboardSnapshot(t *testing.T) {
	d, registry, fake, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	d.Surfaces = []surface.Surface{&failingClaudeListSurface{daemonSurface: fake}}
	d.discoverCatalog(context.Background())
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 2 {
		t.Fatalf("sessions=%d", len(state.Sessions))
	}
	for _, session := range state.Sessions {
		if session.Freshness == nil || !session.Freshness.Stale || session.Freshness.Generation != 2 {
			t.Fatalf("session=%+v", session)
		}
	}
	window, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	staleEvents := 0
	for _, event := range window.Events {
		if event.Type == "session.upserted" && event.EntityID != "" {
			staleEvents++
		}
	}
	if staleEvents < 4 {
		t.Fatalf("catalog events=%d want initial and stale session events", staleEvents)
	}
}

func TestDiscoveryRemovesOnlyAfterTwoSuccessfulOmissions(t *testing.T) {
	d, registry, fake, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	fake.sessions = map[string]surface.Session{}
	d.discoverCatalog(context.Background())
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 2 {
		t.Fatalf("first omission sessions=%d", len(state.Sessions))
	}
	d.discoverCatalog(context.Background())
	state, err = d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 0 {
		t.Fatalf("second omission sessions=%d", len(state.Sessions))
	}
	window, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	removals := 0
	for _, event := range window.Events {
		if event.Type == "session.removed" {
			removals++
		}
	}
	if removals != 2 {
		t.Fatalf("removals=%d", removals)
	}
}

func TestDiscoveryRetainsSessionsOutsideBoundedProviderLists(t *testing.T) {
	for kind := range map[surface.SurfaceKind]struct{}{surface.KindCodex: {}, surface.KindNotion: {}} {
		d, registry, fake, _, _ := daemonFixture(t)
		fake.kind = kind
		for id, session := range fake.sessions {
			session.Surface = kind
			fake.sessions[id] = session
		}
		for index := 0; index < 51; index++ {
			id := fmt.Sprintf("session-%02d", index)
			fake.sessions[id] = surface.Session{ID: id, Surface: kind, Name: id, Status: surface.StatusIdle}
		}
		expectedSessions := len(fake.sessions)
		d.discoverCatalog(context.Background())
		bounded := &boundedCatalogSurface{daemonSurface: fake}
		d.Surfaces = []surface.Surface{bounded}
		for pass := 0; pass < 2; pass++ {
			d.discoverCatalog(context.Background())
		}
		state, err := registry.CatalogSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Sessions) != expectedSessions {
			t.Fatalf("kind=%s retained sessions=%d expected=%d", kind, len(state.Sessions), expectedSessions)
		}
		window, err := registry.CatalogEventsAfter(0, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range window.Events {
			if event.Type == "session.removed" {
				t.Fatalf("kind=%s removed=%+v", kind, event)
			}
		}
	}
}

func TestDiscoveryStreamsFullDashboardSessionRow(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	window, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range window.Events {
		if event.Type != "session.upserted" {
			continue
		}
		var payload struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Session.ID == "" || payload.Session.Capabilities != d.Surfaces[0].Capabilities() || payload.Session.ObservedAt.IsZero() || payload.Session.HostProject == nil || payload.Session.Checkout == nil {
			t.Fatalf("payload=%+v", payload.Session)
		}
		return
	}
	t.Fatal("session.upserted event was not recorded")
}

func TestDiscoveryStreamsProjectionChangesIncludingReturnToPriorValue(t *testing.T) {
	d, registry, _, from, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	initial, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAlias("reviewer", from.ID); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	changed, err := registry.CatalogEventsAfter(initial.LatestSeq, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Events) != 1 || changed.Events[0].Type != "session.upserted" {
		t.Fatalf("changed=%+v", changed.Events)
	}
	var row struct {
		Session dashboardSession `json:"session"`
	}
	if err := json.Unmarshal(changed.Events[0].Payload, &row); err != nil || row.Session.Alias != "reviewer" {
		t.Fatalf("row=%+v err=%v", row.Session, err)
	}
	if err := registry.QueueMessage(from.ID, "pending review"); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	queued, err := registry.CatalogEventsAfter(changed.LatestSeq, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued.Events) != 1 || queued.Events[0].Type != "session.upserted" {
		t.Fatalf("queued=%+v", queued.Events)
	}
	row = struct {
		Session dashboardSession `json:"session"`
	}{}
	if err := json.Unmarshal(queued.Events[0].Payload, &row); err != nil || row.Session.QueueCount != 1 || !row.Session.Current {
		t.Fatalf("row=%+v err=%v", row.Session, err)
	}
	if err := registry.RemoveAlias("reviewer"); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(context.Background())
	returned, err := registry.CatalogEventsAfter(queued.LatestSeq, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(returned.Events) != 1 || returned.Events[0].Type != "session.upserted" {
		t.Fatalf("returned=%+v", returned.Events)
	}
	row = struct {
		Session dashboardSession `json:"session"`
	}{}
	if err := json.Unmarshal(returned.Events[0].Payload, &row); err != nil || row.Session.Alias != "" {
		t.Fatalf("row=%+v err=%v", row.Session, err)
	}
}
