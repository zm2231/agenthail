package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type emptySeedSurface struct {
	*daemonSurface
	reads   atomic.Int32
	seedErr error
	started chan struct{}
}

func (s *emptySeedSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.reads.Add(1)
	if s.seedErr != nil {
		return nil, s.seedErr
	}
	return &surface.SessionReadResult{Items: []surface.TimelineItem{}}, nil
}

func (s *emptySeedSurface) Stream(ctx context.Context, _ *surface.Session, _ string, _ func(surface.StreamEvent), _ time.Duration) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestSessionSourcePersistsSuccessfulEmptySeedAcrossColdRestart(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &emptySeedSurface{daemonSurface: fake, started: make(chan struct{}, 2)}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	first, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cursor != 0 || adapter.reads.Load() != 1 {
		t.Fatalf("first cursor=%d reads=%d", first.Cursor, adapter.reads.Load())
	}
	first.Cancel()
	deadline := time.After(2 * time.Second)
	for {
		manager.mu.Lock()
		count := len(manager.sources)
		manager.mu.Unlock()
		if count == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("source did not become cold")
		case <-time.After(10 * time.Millisecond):
		}
	}
	second, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	if second.Cursor != 0 || adapter.reads.Load() != 1 {
		t.Fatalf("cold empty seed re-read provider: cursor=%d reads=%d", second.Cursor, adapter.reads.Load())
	}
}

func TestSessionSourceDoesNotPersistFailedSeedAsSuccess(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &emptySeedSurface{daemonSurface: fake, started: make(chan struct{}, 2), seedErr: errors.New("provider unavailable")}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	if _, err := manager.prepareStream(context.Background(), &from, adapter); err == nil {
		t.Fatal("failed seed was accepted")
	}
	status, err := reg.SessionJournalSeedStatus(from.ID)
	if err != nil || status != "failed" {
		t.Fatalf("seed status=%q err=%v", status, err)
	}
	if _, err := manager.prepareStream(context.Background(), &from, adapter); err == nil {
		t.Fatal("failed seed did not remain failed")
	}
	if got := adapter.reads.Load(); got != 2 {
		t.Fatalf("failed seed reads=%d, want retry", got)
	}
}

type identitySeedSurface struct {
	*emptySeedSurface
}

func (s *identitySeedSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.reads.Add(1)
	return &surface.SessionReadResult{Items: []surface.TimelineItem{
		{ID: "item", Kind: "text", CallID: "tool-call", TurnID: "turn"},
		{ID: "call-only", Kind: "toolResult", CallID: "tool-only"},
	}}, nil
}

func TestSessionSourceSeedDoesNotPromoteCallIDToTurnID(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &identitySeedSurface{emptySeedSurface: &emptySeedSurface{daemonSurface: fake, started: make(chan struct{}, 1)}}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ItemID == "item" && (payload.TurnID != "turn" || payload.CallID != "tool-call") {
			t.Fatalf("payload=%+v", payload)
		}
		if payload.ItemID == "call-only" && payload.TurnID != "" {
			t.Fatalf("call id promoted to turn: %+v", payload)
		}
	}
}
