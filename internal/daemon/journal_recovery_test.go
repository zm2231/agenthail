package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func journalPayloads(t *testing.T, reg *registry.Registry, sessionID string) []sessionJournalPayload {
	t.Helper()
	page, err := reg.ReadSessionJournalPage(sessionID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	payloads := make([]sessionJournalPayload, 0, len(page.Entries))
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReadJournalPageClearsSourceErrorAfterNewerHealthyActivity(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "recovery", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	source.appendSourceError(errors.New("older failure"))
	source.appendSourceError(errors.New("temporary"))
	page, err := d.readJournalPage(from.ID, 0, 20)
	if err != nil || page.UnavailableReason != "temporary" {
		t.Fatalf("newest source error not reported: page=%+v err=%v", page, err)
	}
	source.append(surface.StreamEvent{ID: "answer", ProviderKey: "answer", Operation: "upsert", Kind: "message", Role: "assistant", Text: "healthy", Final: true})
	page, err = d.readJournalPage(from.ID, 0, 20)
	if err != nil || page.UnavailableReason != "" {
		t.Fatalf("healthy journal still unavailable: page=%+v err=%v", page, err)
	}
}

func TestSeedPreviewDoesNotReplaceRetainedAuthoritativeBody(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "seed", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	full := strings.Repeat("authoritative reply ", 2000)
	source.append(surface.StreamEvent{ID: "reply", ProviderKey: "timeline:reply", Version: uint64(len(full)), Operation: "upsert", Final: true, Kind: "message", Role: "assistant", Text: full})
	before := journalPayloads(t, reg, from.ID)
	if len(before) != 1 || before[0].BodyRef == "" || !before[0].Final {
		t.Fatalf("live row=%+v", before)
	}
	source.appendSeedItems([]surface.TimelineItem{{ID: "reply", Kind: "message", Role: "assistant", Text: full[:4096], Truncated: true}})
	source.appendSeedItems([]surface.TimelineItem{{ID: "reply", Kind: "message", Role: "assistant", Text: full}})
	after := journalPayloads(t, reg, from.ID)
	if len(after) != 1 || after[0].BodyRef != before[0].BodyRef || !after[0].Final || after[0].Version != before[0].Version {
		t.Fatalf("seed replaced authoritative row: before=%+v after=%+v", before[0], after)
	}
	source.appendSeedItems([]surface.TimelineItem{{ID: "reply", Kind: "message", Role: "assistant", Text: "edited by provider"}})
	changed := journalPayloads(t, reg, from.ID)
	if len(changed) != 1 || changed[0].Body != "edited by provider" {
		t.Fatalf("legitimate provider change was skipped: %+v", changed)
	}
}

func TestSeedDoneAndLivePhaseShareOneCompletionRow(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "done", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	const id = "codex:turn-1:done:turn-1"
	source.appendSeedItems([]surface.TimelineItem{{ID: id, Kind: "done", Status: "turn_completed", TurnID: "turn-1"}})
	source.append(surface.StreamEvent{ID: id, ProviderKey: id, Operation: "phase", Kind: "done", TurnID: "turn-1"})
	source.appendSeedItems([]surface.TimelineItem{{ID: id, Kind: "done", Status: "turn_completed", TurnID: "turn-1"}})
	rows := journalPayloads(t, reg, from.ID)
	if len(rows) != 1 || rows[0].Kind != "done" {
		t.Fatalf("completion rows=%+v", rows)
	}
}

type flakySeedSurface struct {
	*daemonSurface
	mu    sync.Mutex
	fail  bool
	items []surface.TimelineItem
	reads atomic.Int32
}

func (s *flakySeedSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.reads.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return nil, errors.New("temporary provider failure")
	}
	return &surface.SessionReadResult{Items: append([]surface.TimelineItem(nil), s.items...)}, nil
}

func (s *flakySeedSurface) set(fail bool, items ...surface.TimelineItem) {
	s.mu.Lock()
	s.fail = fail
	s.items = items
	s.mu.Unlock()
}

func (s *flakySeedSurface) Stream(ctx context.Context, _ *surface.Session, _ string, _ func(surface.StreamEvent), _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

func shortSourceTimers(t *testing.T) {
	t.Helper()
	retry, retryMax, refresh := sessionSeedRetryDelay, sessionSeedRetryMaxDelay, sessionSnapshotRefreshInterval
	sessionSeedRetryDelay, sessionSeedRetryMaxDelay, sessionSnapshotRefreshInterval = 20*time.Millisecond, 40*time.Millisecond, 30*time.Millisecond
	t.Cleanup(func() {
		sessionSeedRetryDelay, sessionSeedRetryMaxDelay, sessionSnapshotRefreshInterval = retry, retryMax, refresh
	})
}

func TestHeldSourceRecoversFromTransientSeedFailure(t *testing.T) {
	shortSourceTimers(t)
	d, reg, fake, from, _ := daemonFixture(t)
	adapter := &flakySeedSurface{daemonSurface: fake}
	adapter.caps.Stream = true
	adapter.set(true)
	manager := newSessionSourceManager(reg)
	t.Cleanup(manager.shutdown)
	first, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cancel()
	page, err := d.readJournalPage(from.ID, 0, 20)
	if err != nil || page.UnavailableReason == "" {
		t.Fatalf("failed seed not reported: page=%+v err=%v", page, err)
	}
	adapter.set(false, surface.TimelineItem{ID: "recovered", Kind: "message", Role: "assistant", Text: "recovered"})
	waitFor(t, 2*time.Second, func() bool {
		status, err := reg.SessionJournalSeedStatus(from.ID)
		return err == nil && status == registry.SessionJournalSeeded
	})
	page, err = d.readJournalPage(from.ID, 0, 20)
	if err != nil || page.UnavailableReason != "" || len(page.Items) != 1 || page.Items[0].Text != "recovered" {
		t.Fatalf("source did not recover: page=%+v err=%v", page, err)
	}
	manager.mu.Lock()
	source := manager.sources[from.ID]
	manager.mu.Unlock()
	if source == nil || source.seedError() != nil {
		t.Fatalf("held source seed error after recovery: %v", source)
	}
}

func TestNonStreamableSourceRefreshesThroughOneSharedProducer(t *testing.T) {
	shortSourceTimers(t)
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &flakySeedSurface{daemonSurface: fake}
	adapter.set(false, surface.TimelineItem{ID: "first", Kind: "message", Role: "assistant", Text: "first"})
	manager := newSessionSourceManager(reg)
	t.Cleanup(manager.shutdown)
	releases := make([]func(), 0, 3)
	for range 3 {
		release, err := manager.hold(&from, adapter, "viewer")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	waitFor(t, 2*time.Second, func() bool { return len(journalPayloads(t, reg, from.ID)) == 1 })
	adapter.set(false, surface.TimelineItem{ID: "first", Kind: "message", Role: "assistant", Text: "first"}, surface.TimelineItem{ID: "second", Kind: "message", Role: "assistant", Text: "second"})
	waitFor(t, 2*time.Second, func() bool { return len(journalPayloads(t, reg, from.ID)) == 2 })
	reads := adapter.reads.Load()
	time.Sleep(100 * time.Millisecond)
	if got := adapter.reads.Load() - reads; got > 5 {
		t.Fatalf("refresh reads per 100ms=%d with three holders", got)
	}
	manager.mu.Lock()
	sources := len(manager.sources)
	manager.mu.Unlock()
	if sources != 1 {
		t.Fatalf("sources=%d", sources)
	}
}

func TestCatalogQueueChangesAdvanceWatermarkAndKeepStaleness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, reg, _, _, to := daemonFixture(t)
	d.discoverCatalog(context.Background())
	_, before, err := reg.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	d.publishCatalogQueueCounts()
	if err := reg.QueueMessage(to.ID, "queued while busy"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.MarkCatalogDiscoveryFailure(to.Surface, "discovery failed", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, failed, err := reg.CatalogState()
	if err != nil || failed <= before {
		t.Fatalf("failure seq=%d before=%d err=%v", failed, before, err)
	}
	d.publishCatalogQueueCounts()
	_, after, err := reg.CatalogState()
	if err != nil || after <= failed {
		t.Fatalf("queue change did not advance catalog watermark: before=%d after=%d err=%v", failed, after, err)
	}
	snapshot, err := reg.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range snapshot.Sessions {
		if record.Session.ID != to.ID {
			continue
		}
		var projection dashboardSession
		if err := json.Unmarshal([]byte(record.ProjectionFingerprint), &projection); err != nil {
			t.Fatal(err)
		}
		if projection.QueueCount != 1 || !record.Freshness.Stale {
			t.Fatalf("projection=%+v freshness=%+v", projection, record.Freshness)
		}
		window, err := reg.CatalogEventsAfter(failed, 10)
		if err != nil || len(window.Events) != 1 || !strings.Contains(string(window.Events[0].Payload), `"queueCount":1`) || !strings.Contains(string(window.Events[0].Payload), `"stale":true`) {
			t.Fatalf("queue event=%+v err=%v", window.Events, err)
		}
		d.publishCatalogQueueCounts()
		if _, unchanged, _ := reg.CatalogState(); unchanged != after {
			t.Fatalf("unchanged queue republished: %d -> %d", after, unchanged)
		}
		return
	}
	t.Fatalf("catalog session %s missing", to.ID)
}

func TestSessionStreamDropsBufferedLiveEntryOlderThanReplayWindow(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	stale := registry.SessionJournalEntry{SessionID: from.ID, Seq: 10, Payload: []byte(`{"itemId":"reply","version":5,"op":"upsert","kind":"text","body":"stale"}`)}
	fresh := registry.SessionJournalEntry{SessionID: from.ID, Seq: 11, Payload: []byte(`{"itemId":"reply","version":9,"op":"upsert","kind":"text","body":"fresh"}`)}
	next := registry.SessionJournalEntry{SessionID: from.ID, Seq: 12, Payload: []byte(`{"itemId":"other","version":1,"op":"upsert","kind":"text","body":"next"}`)}
	entries := make(chan registry.SessionJournalEntry, 3)
	entries <- stale
	entries <- fresh
	entries <- next
	close(entries)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-stream?id="+from.ID, nil)
	d.writeSessionStream(recorder, request, recorder, from.ID, 9, registry.SessionJournalWindow{EarliestSeq: 11, LatestSeq: 11, Entries: []registry.SessionJournalEntry{fresh}}, entries)
	body := recorder.Body.String()
	if strings.Contains(body, `"body":"stale"`) || strings.Count(body, `"body":"fresh"`) != 1 || !strings.Contains(body, `"body":"next"`) {
		t.Fatalf("stream body=%s", body)
	}
}

func TestSessionCreateStarterLauncherMatchesDefaultCreationOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		startErr   error
		status     int
		history    string
		intentDone bool
	}{
		{name: "sent", status: http.StatusCreated, history: "sent"},
		{name: "ambiguous", startErr: surface.DeliveryOutcomeUnknown(errors.New("turn/start timed out")), status: http.StatusAccepted, history: "submitted"},
		{name: "failed", startErr: errors.New("model rejected"), status: http.StatusBadGateway, history: "failed", intentDone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r, fake, _, _ := daemonFixture(t)
			fake.startErr = tc.startErr
			d.SetLaunchers(surface.NewLaunchers([]surface.Surface{fake}))
			w := httptest.NewRecorder()
			d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"codex","launcher":"codex-app-server","message":"hello"}`)))
			if w.Code != tc.status || len(fake.startOptions) != 1 {
				t.Fatalf("status=%d starts=%d body=%s", w.Code, len(fake.startOptions), w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["sessionId"] != "started" || body["launcher"] != surface.LauncherCodexAppServer || body["retryable"] == true {
				t.Fatalf("body=%s", w.Body.String())
			}
			if _, err := r.Session("started"); err != nil {
				t.Fatalf("created session was not registered: %v", err)
			}
			history, err := r.ListHistory(10, "")
			if err != nil || len(history) == 0 || history[0].Kind != tc.history || history[0].SessionID != "started" {
				t.Fatalf("history=%+v err=%v", history, err)
			}
			if tc.startErr == nil {
				if result, ok := body["result"].(map[string]any); !ok || result["uuid"] == nil && result["UUID"] == nil {
					t.Fatalf("send result not preserved: %s", w.Body.String())
				}
				return
			}
			deliveryID, ok := body["deliveryId"].(float64)
			if !ok || deliveryID <= 0 {
				t.Fatalf("delivery id missing: %s", w.Body.String())
			}
			intent, err := r.DeliveryIntent(int64(deliveryID))
			if err != nil || (intent.Status == registry.DeliveryIntentFailed) != tc.intentDone {
				t.Fatalf("intent=%+v err=%v", intent, err)
			}
		})
	}
}

type managedRestartSurface struct {
	*flakySeedSurface
	mode  string
	calls atomic.Int32
}

func (s *managedRestartSurface) Stream(ctx context.Context, _ *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	call := s.calls.Add(1)
	text := "hello"
	if call >= 3 {
		text = "hello world"
	}
	onEvent(surface.StreamEvent{ID: "managed:turn-1:text", ProviderKey: "managed:turn-1:text", Version: uint64(len(text)), Operation: "upsert", TurnID: "turn-1", Kind: "text", Role: "assistant", Text: text})
	switch {
	case call == 1 && s.mode == "transient":
		return errors.New("scripted transient stream failure")
	case call <= 2:
		return surface.ErrStreamWindow
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestManagedStreamRestartsKeepOneAuthoritativeReplyBody(t *testing.T) {
	for _, mode := range []string{"window", "transient"} {
		t.Run(mode, func(t *testing.T) {
			_, reg, fake, from, _ := daemonFixture(t)
			adapter := &managedRestartSurface{flakySeedSurface: &flakySeedSurface{daemonSurface: fake}, mode: mode}
			adapter.caps.Stream = true
			manager := newSessionSourceManager(reg)
			subscription, err := manager.subscribe(&from, adapter)
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Cancel()
			var bodies []string
			waitFor(t, 5*time.Second, func() bool {
				bodies = bodies[:0]
				for _, payload := range journalPayloads(t, reg, from.ID) {
					if payload.Kind == "done" {
						t.Fatalf("restart produced a completion: %+v", payload)
					}
					if payload.ProviderKey == "managed:turn-1:text" {
						bodies = append(bodies, payload.Body)
					}
				}
				return adapter.calls.Load() >= 3 && len(bodies) == 1 && bodies[0] == "hello world"
			})
		})
	}
}

func TestSeedMergesMetadataChangesIntoAuthoritativeRow(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "merge", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	source.append(surface.StreamEvent{ID: "done", ProviderKey: "timeline:done", Operation: "phase", Kind: "done", Status: "completed", TurnID: "turn-1"})
	source.appendSeedItems([]surface.TimelineItem{{ID: "done", Kind: "done", Status: "cancelled", TurnID: "turn-1"}})
	full := strings.Repeat("authoritative reply ", 2000)
	source.append(surface.StreamEvent{ID: "reply", ProviderKey: "timeline:reply", Version: uint64(len(full)), Operation: "upsert", Final: true, Kind: "message", Role: "assistant", Text: full})
	source.appendSeedItems([]surface.TimelineItem{{ID: "reply", Kind: "message", Role: "assistant", Status: "edited", CallID: "call-1", Text: full[:4096], Truncated: true}})
	rows := journalPayloads(t, reg, from.ID)
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	for _, row := range rows {
		switch row.Kind {
		case "done":
			if row.Status != "cancelled" {
				t.Fatalf("status-only change suppressed: %+v", row)
			}
		case "message":
			if row.Status != "edited" || row.CallID != "call-1" || row.BodyRef == "" || !row.Final || row.Version != uint64(len(full)) {
				t.Fatalf("metadata merge lost authority: %+v", row)
			}
		}
	}
}

func TestSkippedSeedKeepsAuthoritativeAppendPrefix(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	writer := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "first", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	writer.append(surface.StreamEvent{ID: "item", ProviderKey: "timeline:item", Version: 11, Operation: "upsert", Final: true, Kind: "message", Role: "assistant", Text: "hello world"})
	restarted := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "second", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	restarted.appendSeedItems([]surface.TimelineItem{{ID: "item", Kind: "message", Role: "assistant", Text: "hello", Truncated: true}})
	restarted.append(surface.StreamEvent{ID: "item", ProviderKey: "timeline:item", Operation: "append", Kind: "message", Role: "assistant", Text: "!"})
	rows := journalPayloads(t, reg, from.ID)
	if len(rows) != 1 || rows[0].Body != "hello world!" {
		t.Fatalf("append prefix lost: %+v", rows)
	}
}

func TestCatalogQueueProjectionReconcilesAfterStaleDiscoveryWrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, reg, _, _, to := daemonFixture(t)
	d.discoverCatalog(context.Background())
	staleRecord := func() registry.CatalogSessionState {
		snapshot, err := reg.CatalogSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range snapshot.Sessions {
			if record.Session.ID == to.ID {
				return record
			}
		}
		t.Fatalf("catalog session %s missing", to.ID)
		return registry.CatalogSessionState{}
	}
	stale := staleRecord()
	if err := reg.QueueMessage(to.ID, "queued during discovery"); err != nil {
		t.Fatal(err)
	}
	d.publishCatalogQueueCounts()
	if _, _, err := reg.RecordCatalogSession(registry.CatalogSessionState{Session: stale.Session, HostProject: stale.HostProject, Checkout: stale.Checkout, ObservedAt: time.Now(), ProjectionFingerprint: stale.ProjectionFingerprint}, registry.CatalogEvent{DedupeKey: "session.upserted:" + to.ID, Type: "session.upserted", EntityID: to.ID, Payload: []byte(`{"session":{}}`)}); err != nil {
		t.Fatal(err)
	}
	d.publishCatalogQueueCounts()
	var projection dashboardSession
	if err := json.Unmarshal([]byte(staleRecord().ProjectionFingerprint), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.QueueCount != 1 {
		t.Fatalf("stale discovery count was not reconciled: %+v", projection)
	}
}
