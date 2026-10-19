package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

func (s *boundedCatalogSurface) CatalogList(ctx context.Context) ([]surface.Session, bool, error) {
	sessions, err := s.daemonSurface.List(ctx)
	if len(sessions) > 50 {
		sessions = sessions[:50]
	}
	return sessions, false, err
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

func TestActiveCatalogSubscriberReceivesTerminalDeliveryProblemOnce(t *testing.T) {
	d, store, _, sender, target := daemonFixture(t)
	if _, _, err := store.QueueDeliveryWithIntent(target.ID, "deliver later", "terminal", surface.SendOptions{SourceSessionID: sender.ID}); err != nil {
		t.Fatal(err)
	}
	_, events, cancel, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	item, err := store.ClaimNextMessage(target.ID, time.Now())
	if err != nil || item == nil {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	if _, err := store.DeadLetterMessage(item.ID, errors.New("target rejected input")); err != nil {
		t.Fatal(err)
	}
	if err := d.catalog.flushCommitted(); err != nil {
		t.Fatal(err)
	}
	event := receiveCatalogEvent(t, events)
	if event.Type != "delivery.problem" {
		t.Fatalf("event=%+v", event)
	}
	if err := store.ReconcileAttentionItems(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.catalog.flushCommitted(); err != nil {
		t.Fatal(err)
	}
	ensureNoCatalogEvent(t, events)
}

func TestCatalogScanFlushesCommittedDeliveryProblemBeforeObserving(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	store, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sender := surface.Session{ID: "sender", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	target := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
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
	if _, err := db.Exec(`UPDATE message_queue SET status='inflight' WHERE id=?`, queueID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeadLetterMessage(queueID, errors.New("target rejected input")); err != nil {
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
	for _, replayed := range window.Events {
		if replayed.Type != "session.queue" {
			t.Fatalf("replayed=%+v", window.Events)
		}
	}
	if _, created, err := d.catalog.publish(registry.CatalogEvent{DedupeKey: "session:sender:after-problem", Type: "session.upserted", EntityID: sender.ID, Payload: []byte(`{"id":"sender"}`)}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	next := receiveCatalogEvent(t, resumed)
	if next.Seq <= first.Seq || next.Type != "session.upserted" {
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
	_ = routeID
	if _, err := store.RecordReplyForwardFailure(sender.ID, target.ID, "reply-turn", "reply body", "target surface unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := d.catalog.flushCommitted(); err != nil {
		t.Fatal(err)
	}
	first := receiveCatalogEvent(t, events)
	if first.Type != "delivery.problem" {
		t.Fatalf("first=%+v", first)
	}
	if _, err := store.RecordReplyForwardFailure(sender.ID, target.ID, "reply-turn", "reply body", "target surface unavailable"); err != nil {
		t.Fatal(err)
	}
	if err := d.catalog.flushCommitted(); err != nil {
		t.Fatal(err)
	}
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
	deadline := time.After(time.Second)
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatal("catalog subscriber closed")
			}
			if event.Type == "session.queue" {
				continue
			}
			return event
		case <-deadline:
			t.Fatal("catalog subscriber did not receive an event")
			return registry.CatalogEvent{}
		}
	}
}

func ensureNoCatalogEvent(t *testing.T, events <-chan registry.CatalogEvent) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case event := <-events:
			if event.Type != "session.queue" {
				t.Fatalf("unexpected catalog event=%+v", event)
			}
		case <-deadline:
			return
		}
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
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
				return
			}
			if event.Type == "session.queue" {
				continue
			}
			result <- event
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
	d, store, fake, _, _ := daemonFixture(t)
	d.discoverCatalog(context.Background())
	fake.sessions = map[string]surface.Session{}
	d.discoverCatalog(context.Background())
	history, err := d.dashboardState(context.Background(), registry.CatalogPageRequest{Scope: "all", Limit: 50, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Sessions) != 2 || history.Sessions[0].Status != surface.StatusOffline {
		t.Fatalf("first omission kept %d sessions in history, want both offline", len(history.Sessions))
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 0 {
		t.Fatalf("first omission left %d offline sessions in the current snapshot", len(state.Sessions))
	}
	d.discoverCatalog(context.Background())
	history, err = d.dashboardState(context.Background(), registry.CatalogPageRequest{Scope: "all", Limit: 50, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Sessions) != 0 {
		t.Fatalf("second omission history sessions=%d", len(history.Sessions))
	}
	state, err = d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 0 {
		t.Fatalf("second omission sessions=%d", len(state.Sessions))
	}
	window, err := store.CatalogEventsAfter(0, 20)
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
		if payload.Session.ID == "" || payload.Session.Capabilities != d.Surfaces[0].Capabilities() || payload.Session.ObservedAt.IsZero() || payload.Session.Checkout == nil {
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
	queued.Events = withoutQueueCatalogEvents(queued.Events)
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

func withoutQueueCatalogEvents(events []registry.CatalogEvent) []registry.CatalogEvent {
	filtered := make([]registry.CatalogEvent, 0, len(events))
	for _, event := range events {
		if event.Type != "session.queue" {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func TestDiscoveryProjectsSubagentIdentityAndRollup(t *testing.T) {
	d, registry, fake, from, to := daemonFixture(t)
	from.Subagents = &surface.SubagentRollup{Count: 3, Working: 1}
	to.Subagent = &surface.Subagent{ParentID: from.ID, RootID: from.ID, Depth: 1, Nickname: "Ada", Role: "reviewer"}
	to.Status = surface.StatusBusy
	fake.sessions = map[string]surface.Session{from.ID: from, to.ID: to}
	d.discoverCatalog(context.Background())

	window, err := registry.CatalogEventsAfter(0, 20)
	if err != nil {
		t.Fatal(err)
	}
	streamed := map[string]dashboardSession{}
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
		streamed[payload.Session.ID] = payload.Session
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]dashboardSession{}
	for _, session := range state.Sessions {
		snapshot[session.ID] = session
	}
	for name, rows := range map[string]map[string]dashboardSession{"stream": streamed, "snapshot": snapshot} {
		if got := rows[to.ID].Subagent; got == nil || *got != *to.Subagent {
			t.Fatalf("%s subagent row = %+v", name, got)
		}
		if got := rows[from.ID].Subagents; got == nil || *got != *from.Subagents {
			t.Fatalf("%s parent rollup = %+v", name, got)
		}
		if rows[from.ID].Subagent != nil || rows[to.ID].Subagents != nil {
			t.Fatalf("%s rows carry identity they do not own: %+v %+v", name, rows[from.ID], rows[to.ID])
		}
	}
}

func TestDiscoveryNestsARowStoredWithoutItsParentOnceTheParentIsReported(t *testing.T) {
	d, registry, fake, from, _ := daemonFixture(t)
	child := surface.Session{ID: "spawned-child", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "child", Status: surface.StatusIdle, Source: "subAgent", Transport: "desktop"}
	if err := registry.RegisterSession(child); err != nil {
		t.Fatal(err)
	}
	child.Subagent = &surface.Subagent{ParentID: from.ID, RootID: from.ID, Depth: 1}
	fake.sessions[child.ID] = child
	d.discoverCatalog(context.Background())
	stored, err := registry.Session(child.ID)
	if err != nil || stored.Subagent == nil || stored.Subagent.ParentID != from.ID {
		t.Fatalf("stored child=%+v err=%v, want nested under %s", stored, err, from.ID)
	}
	state, err := registry.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range state.Sessions {
		if record.Session.ID == child.ID && (record.Session.Subagent == nil || record.Session.Subagent.ParentID != from.ID) {
			t.Fatalf("catalog child=%+v, want nested under %s", record.Session, from.ID)
		}
	}
}

func TestSnapshotListsTopLevelSessionsAndOnlyActiveSubagents(t *testing.T) {
	d, store, fake, lead, _ := daemonFixture(t)
	lead.Subagents = &surface.SubagentRollup{Count: 2, Working: 1}
	child := func(id, parent string, status surface.SessionStatus) surface.Session {
		return surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: id, Status: status, Source: "subAgent", Transport: "desktop", Subagent: &surface.Subagent{ParentID: parent, RootID: lead.ID, Depth: 1}}
	}
	working := child("working-child", lead.ID, surface.StatusBusy)
	resting := child("resting-child", lead.ID, surface.SessionStatus("notLoaded"))
	grandchild := child("resting-grandchild", resting.ID, surface.SessionStatus("notLoaded"))
	orphan := child("orphan-child", "parent-codex-never-listed", surface.StatusBusy)
	fake.sessions = map[string]surface.Session{lead.ID: lead, working.ID: working, resting.ID: resting, grandchild.ID: grandchild, orphan.ID: orphan}
	d.discoverCatalog(context.Background())
	if err := store.RegisterSession(surface.Session{ID: "parentless-subagent", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "subAgent", Transport: "desktop"}); err != nil {
		t.Fatal(err)
	}

	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]dashboardSession{}
	for _, session := range state.Sessions {
		listed[session.ID] = session
	}
	for _, hidden := range []string{resting.ID, grandchild.ID, orphan.ID, "parentless-subagent"} {
		if _, found := listed[hidden]; found {
			t.Fatalf("snapshot lists %s; it carries only top-level sessions and active subagents under a listed parent", hidden)
		}
	}
	if _, found := listed[working.ID]; !found {
		t.Fatal("snapshot dropped a working subagent")
	}
	if got := listed[lead.ID].Subagents; got == nil || *got != (surface.SubagentRollup{Count: 4, Working: 1}) {
		t.Fatalf("lead rollup=%+v, want its own two plus the two resting descendants", got)
	}

	page, err := d.dashboardState(context.Background(), registry.CatalogPageRequest{Scope: "all", Limit: 50, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	paged := map[string]dashboardSession{}
	for _, session := range page.Sessions {
		paged[session.ID] = session
	}
	if _, found := paged[resting.ID]; found || page.TotalSessions != len(page.Sessions) {
		t.Fatalf("page lists hidden subagents or miscounts: total=%d rows=%d", page.TotalSessions, len(page.Sessions))
	}
	if got := paged[lead.ID].Subagents; got == nil || got.Count != 4 {
		t.Fatalf("paged lead rollup=%+v", got)
	}

	family, err := d.dashboardState(context.Background(), registry.CatalogPageRequest{ParentID: lead.ID, Limit: 50, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	children := map[string]dashboardSession{}
	for _, session := range family.Sessions {
		children[session.ID] = session
	}
	if len(children) != 2 || children[resting.ID].ID == "" || children[working.ID].ID == "" || children[orphan.ID].ID != "" {
		t.Fatalf("parent page=%v, want every direct subagent of the lead", children)
	}
	if got := children[resting.ID].Subagents; got == nil || got.Count != 1 {
		t.Fatalf("resting child rollup=%+v, want its resting grandchild", got)
	}

	response := serveDashboardRequest(dashboardRouter(d), http.MethodGet, "/api/v1/snapshot?limit=50&parentId="+working.ID, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sessions":[]`) {
		t.Fatalf("empty parent page status=%d body=%.300s, want an empty sessions array", response.Code, response.Body.String())
	}
}

func TestCatalogStreamShowsDevicesTheSessionListAndHostsEveryRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, store, fake, lead, _ := daemonFixture(t)
	child := func(id, parent string, status surface.SessionStatus) surface.Session {
		return surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: id, Status: status, Source: "subAgent", Transport: "desktop", Subagent: &surface.Subagent{ParentID: parent, RootID: lead.ID, Depth: 1}}
	}
	working, resting := child("working-child", lead.ID, surface.StatusBusy), child("resting-child", lead.ID, surface.SessionStatus("notLoaded"))
	buried := child("busy-under-resting", resting.ID, surface.StatusBusy)
	fake.sessions = map[string]surface.Session{lead.ID: lead, working.ID: working, resting.ID: resting, buried.ID: buried}
	d.discoverCatalog(context.Background())
	d.publishCatalogPresence()
	latestSeq := func() uint64 {
		t.Helper()
		_, seq, err := store.CatalogState()
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	latest := latestSeq()

	token := func(name string, scopes []string) string {
		pairing, err := store.CreateDevicePairing(name, scopes, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, secret, err := store.CompleteDevicePairing(pairing.Secret, name)
		if err != nil {
			t.Fatal(err)
		}
		return secret
	}
	deviceToken := token("Phone", []string{"read", "control"})
	hostToken := token("Agenthail host", []string{"read", "control", registry.HostScope})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "dashboard-secret"}))
	defer server.Close()
	stream := func(bearer string, after, until uint64) []catalogStreamEnvelope {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/catalog-events?after=%d", server.URL, after), nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+bearer)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("catalog-events status=%d", response.StatusCode)
		}
		reader := bufio.NewReader(response.Body)
		var events []catalogStreamEnvelope
		for len(events) == 0 || events[len(events)-1].Seq < until {
			events = append(events, receiveCatalogSSE(t, reader))
		}
		return events
	}
	type seen struct {
		types        map[string]string
		leadUpserted bool
		subagents    *surface.SubagentRollup
	}
	read := func(events []catalogStreamEnvelope) seen {
		result := seen{types: map[string]string{}}
		for index, event := range events {
			if index > 0 && event.Seq <= events[index-1].Seq {
				t.Fatalf("sequence did not increase: %d after %d", event.Seq, events[index-1].Seq)
			}
			var data struct {
				SessionID string           `json:"sessionId"`
				Session   dashboardSession `json:"session"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			id := data.SessionID
			if event.Type == "session.upserted" {
				id = data.Session.ID
				if id == lead.ID {
					result.leadUpserted, result.subagents = true, data.Session.Subagents
				}
			}
			if id != "" {
				result.types[id] = event.Type
			}
		}
		return result
	}
	snapshotIDs := func(bearer string) map[string]bool {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/snapshot?fresh=1", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+bearer)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var state struct {
			Sessions []dashboardSession `json:"sessions"`
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&state) != nil {
			t.Fatalf("snapshot status=%d", response.StatusCode)
		}
		ids := map[string]bool{}
		for _, session := range state.Sessions {
			ids[session.ID] = true
		}
		return ids
	}

	device := read(stream(deviceToken, 0, latest))
	if device.types[resting.ID] != "session.removed" || device.types[buried.ID] != "session.removed" || device.types[working.ID] != "session.upserted" {
		t.Fatalf("device stream=%v, want the subagents under a resting one removed and the working one listed", device.types)
	}
	if device.subagents == nil || *device.subagents != (surface.SubagentRollup{Count: 2, Working: 1}) {
		t.Fatalf("device lead rollup=%+v, want the resting subagent and its busy child counted", device.subagents)
	}
	host := read(stream(hostToken, 0, latest))
	if host.types[resting.ID] != "session.upserted" || host.types[buried.ID] != "session.upserted" || host.types[working.ID] != "session.upserted" {
		t.Fatalf("host stream=%v, want every row", host.types)
	}
	if ids := snapshotIDs(deviceToken); ids[resting.ID] || ids[buried.ID] || !ids[working.ID] {
		t.Fatalf("device snapshot=%v, want the session list", ids)
	}
	if ids := snapshotIDs(hostToken); !ids[resting.ID] || !ids[buried.ID] || !ids[working.ID] {
		t.Fatalf("host snapshot=%v, want every row", ids)
	}
	if err := os.MkdirAll(filepath.Dir(DashboardConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DashboardConfigPath(), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/snapshot?fresh=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+hostToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		t.Fatal("host snapshot served the cached session list when the refresh failed; a host must see every row or an error")
	}
	if err := os.Remove(DashboardConfigPath()); err != nil {
		t.Fatal(err)
	}

	resting.Status = surface.StatusBusy
	fake.sessions[resting.ID] = resting
	d.discoverCatalog(context.Background())
	d.publishCatalogPresence()
	reopened := read(stream(deviceToken, latest, latestSeq()))
	if reopened.types[resting.ID] != "session.upserted" || reopened.types[buried.ID] != "session.upserted" {
		t.Fatalf("device stream after the subagent resumed=%v, want it and its already busy child listed", reopened.types)
	}
	if !reopened.leadUpserted || reopened.subagents != nil {
		t.Fatalf("lead rollup after its subagents were listed=%+v (upserted=%v), want none hidden", reopened.subagents, reopened.leadUpserted)
	}
}

func TestSnapshotKeepsTheSubagentCountStoredBeforeHiddenCounts(t *testing.T) {
	d, store, fake, lead, _ := daemonFixture(t)
	lead.Subagents = &surface.SubagentRollup{Count: 3, Working: 1}
	fake.sessions = map[string]surface.Session{lead.ID: lead}
	d.discoverCatalog(context.Background())
	db, err := sql.Open("sqlite", store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE catalog_sessions SET projection_fingerprint=json_remove(projection_fingerprint,'$.observedSubagents','$.hiddenSubagents','$.listed') WHERE session_id=?`, lead.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=18`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := registry.Open(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	d = New(reopened, []surface.Surface{&daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}}})
	d.publishCatalogPresence()
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range state.Sessions {
		if session.ID == lead.ID {
			if session.Subagents == nil || *session.Subagents != (surface.SubagentRollup{Count: 3, Working: 1}) {
				t.Fatalf("lead rollup=%+v, want the stored count", session.Subagents)
			}
			return
		}
	}
	t.Fatal("snapshot dropped the lead")
}
