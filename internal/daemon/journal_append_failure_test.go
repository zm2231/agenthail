package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func rejectJournalWrites(t *testing.T, reg *registry.Registry, providerKey string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", reg.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`CREATE TRIGGER reject_journal_insert BEFORE INSERT ON session_journal WHEN NEW.provider_key='` + providerKey + `' BEGIN SELECT RAISE(ABORT, 'injected journal rejection'); END`,
		`CREATE TRIGGER reject_journal_update BEFORE UPDATE ON session_journal WHEN NEW.provider_key='` + providerKey + `' BEGIN SELECT RAISE(ABORT, 'injected journal rejection'); END`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		for _, statement := range []string{`DROP TRIGGER reject_journal_insert`, `DROP TRIGGER reject_journal_update`} {
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func journalRows(t *testing.T, reg *registry.Registry, sessionID string) (map[string]sessionJournalPayload, []string) {
	t.Helper()
	rows := map[string]sessionJournalPayload{}
	var sourceErrors []string
	for _, payload := range journalPayloads(t, reg, sessionID) {
		if payload.Kind == "source-error" {
			sourceErrors = append(sourceErrors, payload.Reason)
			continue
		}
		rows[payload.ProviderKey] = payload
	}
	return rows, sourceErrors
}

func requireJournalFailureEvidence(t *testing.T, reg *registry.Registry, sessionID string, sourceErrors []string) {
	t.Helper()
	if len(sourceErrors) == 0 || !strings.Contains(sourceErrors[len(sourceErrors)-1], "injected journal rejection") {
		t.Fatalf("failed journal append left no source-error evidence: %q", sourceErrors)
	}
	status, err := reg.SessionJournalSeedStatus(sessionID)
	if err != nil || status != registry.SessionJournalSeedFailed {
		t.Fatalf("failed journal append did not force a reseed: status=%q err=%v", status, err)
	}
}

func TestJournalAppendFailureDoesNotAdvanceCursor(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "small", text: "must persist"},
		{name: "large", text: strings.Repeat("large reply ", sessionStreamBodyBytes/4)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, reg, fake, from, _ := daemonFixture(t)
			source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "cursor", appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
			allow := rejectJournalWrites(t, reg, "cursor-key")
			event := surface.StreamEvent{ID: "cursor-key", ProviderKey: "cursor-key", Cursor: 1, Operation: "upsert", Kind: "message", Role: "assistant", Text: tc.text}
			source.append(event)
			rows, _ := journalRows(t, reg, from.ID)
			if _, ok := rows["cursor-key"]; ok {
				t.Fatalf("rejected row was persisted: %+v", rows)
			}
			allow()
			source.append(event)
			rows, sourceErrors := journalRows(t, reg, from.ID)
			row, ok := rows["cursor-key"]
			if !ok {
				t.Fatalf("replayed cursor was dropped after failed append: %+v", rows)
			}
			if len(tc.text) > sessionStreamBodyBytes {
				if row.BodyRef == "" || !row.Truncated {
					t.Fatalf("large replay lost full body reference: %+v", row)
				}
				body, _, err := reg.SessionJournalBody(from.ID, row.BodyRef, 0, len(tc.text))
				if err != nil || string(body) != tc.text {
					t.Fatalf("large replay body len=%d err=%v", len(body), err)
				}
			} else if row.Body != tc.text {
				t.Fatalf("replayed body=%q", row.Body)
			}
			requireJournalFailureEvidence(t, reg, from.ID, sourceErrors)
		})
	}
}

func TestJournalAppendDeltaFailureDoesNotCorruptAccumulatedBody(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "delta", appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
	source.append(surface.StreamEvent{ID: "delta-key", ProviderKey: "delta-key", Kind: "message", Role: "assistant", Text: "Hello"})
	allow := rejectJournalWrites(t, reg, "delta-key")
	source.append(surface.StreamEvent{ID: "delta-key", ProviderKey: "delta-key", Kind: "message", Role: "assistant", Text: " world"})
	rows, _ := journalRows(t, reg, from.ID)
	if rows["delta-key"].Body != "Hello" {
		t.Fatalf("rejected delta changed the journal row: %+v", rows["delta-key"])
	}
	allow()
	source.append(surface.StreamEvent{ID: "delta-key", ProviderKey: "delta-key", Kind: "message", Role: "assistant", Text: " world"})
	rows, sourceErrors := journalRows(t, reg, from.ID)
	if rows["delta-key"].Body != "Hello world" {
		t.Fatalf("retried delta body=%q", rows["delta-key"].Body)
	}
	requireJournalFailureEvidence(t, reg, from.ID, sourceErrors)
}

func TestJournalUpsertFailureKeepsPersistedBodyForLaterDeltas(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "upsert", appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
	source.append(surface.StreamEvent{ID: "upsert-key", ProviderKey: "upsert-key", Operation: "upsert", Kind: "message", Role: "assistant", Text: "draft"})
	allow := rejectJournalWrites(t, reg, "upsert-key")
	source.append(surface.StreamEvent{ID: "upsert-key", ProviderKey: "upsert-key", Operation: "upsert", Kind: "message", Role: "assistant", Text: "rejected rewrite"})
	allow()
	source.append(surface.StreamEvent{ID: "upsert-key", ProviderKey: "upsert-key", Kind: "message", Role: "assistant", Text: " continued"})
	rows, sourceErrors := journalRows(t, reg, from.ID)
	if rows["upsert-key"].Body != "draft continued" {
		t.Fatalf("delta built on an unpersisted upsert: body=%q", rows["upsert-key"].Body)
	}
	requireJournalFailureEvidence(t, reg, from.ID, sourceErrors)
}

func TestJournalSeedFailureThenLiveHandoffDoesNotDuplicate(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "handoff", appendBodies: map[string]string{}, appendCursors: map[string]uint64{}}
	allow := rejectJournalWrites(t, reg, "timeline:reply")
	source.appendSeedItems([]surface.TimelineItem{{ID: "reply", Kind: "message", Role: "assistant", Text: "seeded"}})
	rows, _ := journalRows(t, reg, from.ID)
	if _, ok := rows["timeline:reply"]; ok {
		t.Fatalf("rejected seed row was persisted: %+v", rows)
	}
	source.append(surface.StreamEvent{ID: "reply", ProviderKey: "timeline:reply", Kind: "message", Role: "assistant", Text: " live"})
	allow()
	source.appendSeedItems([]surface.TimelineItem{{ID: "reply", Kind: "message", Role: "assistant", Text: "seeded"}})
	source.append(surface.StreamEvent{ID: "reply", ProviderKey: "timeline:reply", Kind: "message", Role: "assistant", Text: " live"})
	rows, sourceErrors := journalRows(t, reg, from.ID)
	if rows["timeline:reply"].Body != "seeded live" {
		t.Fatalf("seed/live handoff body=%q", rows["timeline:reply"].Body)
	}
	requireJournalFailureEvidence(t, reg, from.ID, sourceErrors)
}

type replayingStreamSurface struct {
	*flakySeedSurface
	replay chan struct{}
	calls  atomic.Int32
}

func (s *replayingStreamSurface) Stream(ctx context.Context, _ *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	onEvent(surface.StreamEvent{ID: "replayed", ProviderKey: "replayed", Cursor: 1, Operation: "upsert", Kind: "message", Role: "assistant", Text: "must persist"})
	if s.calls.Add(1) == 1 {
		select {
		case <-s.replay:
			return surface.ErrStreamWindow
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestDaemonSessionActivityRecoversReplayAfterTransientJournalFailure(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &replayingStreamSurface{flakySeedSurface: &flakySeedSurface{daemonSurface: fake}, replay: make(chan struct{})}
	adapter.caps.Stream = true
	allow := rejectJournalWrites(t, reg, "replayed")
	d := New(reg, []surface.Surface{adapter})
	defer d.sources.shutdown()
	subscription, err := d.sources.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	waitFor(t, 5*time.Second, func() bool {
		_, sourceErrors := journalRows(t, reg, from.ID)
		return len(sourceErrors) > 0
	})
	allow()
	close(adapter.replay)
	readTimeline := func() surface.SessionTimeline {
		response := httptest.NewRecorder()
		d.dashboardSessionHandler(response, httptest.NewRequest(http.MethodGet, "/api/session?id="+from.ID+"&timeline=1", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			Timeline surface.SessionTimeline `json:"timeline"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Timeline
	}
	var timeline surface.SessionTimeline
	waitFor(t, 5*time.Second, func() bool {
		timeline = readTimeline()
		return adapter.calls.Load() >= 2 && len(timeline.Items) > 0
	})
	if len(timeline.Items) != 1 || timeline.Items[0].Text != "must persist" || timeline.Items[0].Role != "assistant" {
		t.Fatalf("session activity after replay=%+v", timeline)
	}
	_, sourceErrors := journalRows(t, reg, from.ID)
	if len(sourceErrors) == 0 || !strings.Contains(sourceErrors[0], "injected journal rejection") {
		t.Fatalf("failed append left no source-error evidence: %q", sourceErrors)
	}
}
